package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
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
  4) agy          Antigravity (agy-notify plugin; clipboard transport pending)
  5) all          everything above (Antigravity clipboard excluded until resolved)
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

// valueFlags are the connect/setup flags that consume the FOLLOWING token as
// their value (space form, e.g. "--port 18339"), so hostFromArgs does not
// mistake that value for the host. In connect/setup only --port (getPort ->
// getFlag) and --local-bin (resolveLocalBinary -> getFlag) take a value; every
// other connect/setup flag is boolean. --to is an `update` flag (update.go
// FlagSet), NOT a connect/setup flag, so it is intentionally absent here.
var valueFlags = map[string]bool{"--port": true, "--local-bin": true}

// errNoHost is returned by hostFromArgs when args carry no positional host token.
var errNoHost = errors.New("missing <host>: usage: cc-clip <connect|setup> <host> [flags]")

// hostFromArgs returns the first positional (non-flag) token in args, where args
// is os.Args[2:] (everything after the subcommand). It tolerates flags appearing
// before the host (e.g. `connect --codex myhost`), replacing the old positional
// os.Args[2] assumption, by skipping --flag, --flag=value, the value token after
// a space-form value flag, and short -x flags. Returns errNoHost when no
// positional token is present.
func hostFromArgs(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			// "--flag=value" carries its value inline; "--flag value" (a known
			// value flag) consumes the next token so it is not read as the host.
			if !strings.ContainsRune(a, '=') && valueFlags[a] && i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "-") { // short flags (e.g. -v) are not a host
			continue
		}
		return a, nil
	}
	return "", errNoHost
}

// stdinIsTTYReader reports whether r is an interactive terminal. Only an
// *os.File backed by a character device qualifies; any other reader (pipe,
// redirected file, or a test's bytes.Reader) is treated as non-interactive so
// an unattended run takes the non-TTY fallback instead of blocking on the
// interactive target menu. Avoids a golang.org/x/term dependency (design §0).
func stdinIsTTYReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// stdinIsTTY reports whether the process stdin is an interactive terminal.
func stdinIsTTY() bool { return stdinIsTTYReader(os.Stdin) }

// errHookControlNonClaude is returned when --no-hooks/--hooks is combined with a
// target set that does not include Claude. Those flags govern ONLY the Claude
// Code settings.json managed hooks, so they are meaningless (and rejected) for
// codex/opencode/agy-only runs. Under --all, Claude IS selected, so it is
// allowed. Callers print it to stderr and exit 2 (design §6(b)).
var errHookControlNonClaude = errors.New(
	"--no-hooks/--hooks only apply to the Claude target; drop them or include --claude/--all")

// checkHookControlTargets enforces that --no-hooks/--hooks is Claude-scoped. It
// returns errHookControlNonClaude when a hook-control flag is set but Claude is
// not among the selected targets, and nil otherwise.
func checkHookControlTargets(t DeployTargets, noHooks, hooks bool) error {
	if (noHooks || hooks) && !t.Claude {
		return errHookControlNonClaude
	}
	return nil
}

// legacyCodexNotice is the one-time breaking-change notice printed when a run
// uses the legacy single --codex selector (not --all). As of v0.9.0, --codex
// installs ONLY Codex transport (X11 + codex-notify) and no longer installs the
// Claude shim; --all restores the previous full behavior.
const legacyCodexNotice = "cc-clip: v0.9.0 BREAKING: --codex now installs ONLY Codex (X11 + codex-notify); it no longer installs the Claude shim. Use --all for the previous full behavior. (set CC_CLIP_NO_DEPRECATION_NOTICE=1 to silence this notice)"

// maybeLegacyCodexNotice writes legacyCodexNotice to errOut (STDERR in
// production, so scripted stdout parsing is unaffected) exactly when the run
// used the bare legacy --codex selector — i.e. --codex present, --all absent,
// and the resolved targets are Codex-only. Setting CC_CLIP_NO_DEPRECATION_NOTICE
// to any non-empty value suppresses the notice ONLY; it does not change
// --codex's install semantics (design §18.5).
func maybeLegacyCodexNotice(errOut io.Writer, args []string, t DeployTargets) {
	if os.Getenv("CC_CLIP_NO_DEPRECATION_NOTICE") != "" {
		return
	}
	if flagInArgs(args, "codex") && !flagInArgs(args, "all") && t == (DeployTargets{Codex: true}) {
		fmt.Fprintln(errOut, legacyCodexNotice)
	}
}

// claudeTargeted reports whether the Claude integration (clipboard shim +
// claude-notify in ~/.claude/settings.json) is selected. Under --all this is
// true because parseDeployTargets/menuSelection set every field.
func claudeTargeted(t DeployTargets) bool { return t.Claude }

// codexTargeted reports whether the Codex integration (Xvfb + x11-bridge +
// codex-notify in ~/.codex/config.toml) is selected.
func codexTargeted(t DeployTargets) bool { return t.Codex }

// shimTargeted reports whether this run should install the clipboard shim. The
// xclip/wl-paste shim serves Claude Code AND opencode (both terminal tools that
// shell out to the real binary); Codex reads X11 directly and agy is notify-only,
// so neither needs the shim. A run that does not target the shim SKIPS install
// but must never uninstall an existing shim (design §3 + Option A).
func shimTargeted(t DeployTargets) bool { return t.Claude || t.Opencode }
