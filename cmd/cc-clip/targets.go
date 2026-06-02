package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// DeployTargets is the set of integration targets a connect/setup run installs.
// The axes are orthogonal — a run may select several (via --all) — but the CLI
// surface accepts at most one DISTINCT target selector per invocation. The zero
// value targets nothing; callers apply their own default when parseDeployTargets
// reports explicit=false.
type DeployTargets struct {
	Claude      bool
	Codex       bool
	Opencode    bool
	Antigravity bool // canonical CLI flag is --agy (alias --antigravity)
}

// Any reports whether at least one target is selected.
func (t DeployTargets) Any() bool {
	return t.Claude || t.Codex || t.Opencode || t.Antigravity
}

// errMultipleTargets is returned when more than one DISTINCT deployment target is
// selected (e.g. --codex --all, or --claude --codex). Callers print it to stderr
// and exit 2. It is a sentinel so tests can match with errors.Is.
var errMultipleTargets = errors.New(
	"only one deployment target may be given: choose one of --claude / --codex / --opencode / --agy / --all (use --all for everything)")

// parseDeployTargets resolves the deployment-target selector flags from args.
// --agy is the canonical Antigravity flag with --antigravity as an accepted
// alias; --all selects every target. It performs no SSH/IO and fails fast:
//
//   - explicit=false (zero DeployTargets, nil err) when NO target flag is present
//     — the caller then applies its own default (connect/setup both default to
//     {Claude}; a TTY may instead present the interactive menu).
//   - explicit=true with the selected target when exactly one DISTINCT target is
//     given.
//   - errMultipleTargets when more than one distinct target is selected.
//
// Distinctness matters for the Antigravity alias: --agy --antigravity is ONE
// target (no error), while --codex --all or --claude --codex are two (error).
func parseDeployTargets(args []string) (t DeployTargets, explicit bool, err error) {
	claude := flagInArgs(args, "claude")
	codex := flagInArgs(args, "codex")
	opencode := flagInArgs(args, "opencode")
	antigravity := flagInArgs(args, "agy") || flagInArgs(args, "antigravity")
	all := flagInArgs(args, "all")

	selected := 0
	for _, on := range []bool{claude, codex, opencode, antigravity, all} {
		if on {
			selected++
		}
	}

	switch {
	case selected == 0:
		return DeployTargets{}, false, nil
	case selected > 1:
		return DeployTargets{}, false, errMultipleTargets
	case all:
		return DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}, true, nil
	case claude:
		return DeployTargets{Claude: true}, true, nil
	case codex:
		return DeployTargets{Codex: true}, true, nil
	case opencode:
		return DeployTargets{Opencode: true}, true, nil
	default: // antigravity
		return DeployTargets{Antigravity: true}, true, nil
	}
}

// flagInArgs reports whether a boolean flag --name (or --name=<bool>) is present
// and enabled in args. It mirrors hasFlag's matching semantics but operates on a
// caller-supplied slice so parseDeployTargets stays pure and unit-testable.
func flagInArgs(args []string, name string) bool {
	prefix := "--" + name + "="
	for _, arg := range args {
		if arg == "--"+name {
			return true
		}
		if strings.HasPrefix(arg, prefix) {
			enabled, perr := strconv.ParseBool(strings.TrimPrefix(arg, prefix))
			if perr != nil {
				return true
			}
			return enabled
		}
	}
	return false
}

// targetMenu is the interactive target chooser shown (per design §5) when no
// target flag was given on a TTY. It must be rendered BEFORE any SSH/daemon
// activity so the user picks before a passphrase prompt.
const targetMenu = `Select deployment target:
  1) claude       clipboard shim + claude-notify
  2) codex        X11 bridge + codex-notify
  3) opencode     clipboard shim only (no Claude/Codex config changes)
  4) antigravity  antigravity-notify plugin (clipboard transport pending)
  5) all          everything above (antigravity clipboard excluded until resolved)
`

// maxMenuAttempts bounds invalid-input re-prompts so a misbehaving stream cannot
// loop forever; after the cap the caller's default is applied.
const maxMenuAttempts = 3

// menuSelection maps a raw menu choice (1-5, surrounding whitespace tolerated) to
// DeployTargets. ok=false for any unrecognized choice.
func menuSelection(choice string) (DeployTargets, bool) {
	switch strings.TrimSpace(choice) {
	case "1":
		return DeployTargets{Claude: true}, true
	case "2":
		return DeployTargets{Codex: true}, true
	case "3":
		return DeployTargets{Opencode: true}, true
	case "4":
		return DeployTargets{Antigravity: true}, true
	case "5":
		return DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}, true
	default:
		return DeployTargets{}, false
	}
}

// promptDeployTargets renders the target menu to out and reads a 1-5 selection
// from in, re-prompting on invalid input up to maxMenuAttempts. It returns the
// chosen targets with ok=true on a valid selection, or ok=false on EOF/read
// error or exhausted attempts so the caller applies its own default.
func promptDeployTargets(in io.Reader, out io.Writer) (DeployTargets, bool) {
	fmt.Fprint(out, targetMenu)
	scanner := bufio.NewScanner(in)
	for attempt := 0; attempt < maxMenuAttempts; attempt++ {
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			return DeployTargets{}, false // EOF / read error
		}
		if t, ok := menuSelection(scanner.Text()); ok {
			return t, true
		}
		fmt.Fprintln(out, "  invalid selection; enter a number 1-5")
	}
	return DeployTargets{}, false
}

// resolveImplicitTargets is called when parseDeployTargets reported explicit=false
// (no target flag present). On a TTY it presents the menu; on a non-TTY it falls
// back to fallback and writes a one-line, non-blocking warning to errOut naming
// fallbackLabel. A TTY user who declines (EOF/invalid) also gets the fallback.
// Callers MUST pass a minimal-side-effect fallback ({Claude}) so an unattended
// run never silently triggers high-side-effect installs (Xvfb/sudo/consent).
func resolveImplicitTargets(isTTY bool, in io.Reader, out, errOut io.Writer, fallback DeployTargets, fallbackLabel string) DeployTargets {
	if isTTY {
		if t, ok := promptDeployTargets(in, out); ok {
			return t
		}
		fmt.Fprintf(out, "no selection made; defaulting to %s\n", fallbackLabel)
		return fallback
	}
	fmt.Fprintf(errOut, "cc-clip: no target flag given and stdin is not a TTY; defaulting to %s (pass --claude/--codex/--opencode/--agy/--all to choose explicitly)\n", fallbackLabel)
	return fallback
}
