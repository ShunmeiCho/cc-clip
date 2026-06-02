package main

import (
	"errors"
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
