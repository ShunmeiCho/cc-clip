package main

import "fmt"

// daemonVersionNotice compares the RUNNING daemon's /health version against
// this binary's and returns a warning line when they demonstrably differ, or
// "" when no honest claim can be made (#22 P2).
//
// Silence over guessing: an empty runningVersion means a pre-#22 daemon and
// an empty ownVersion means a dev build — in both cases a mismatch cannot be
// proven, and a false "stale daemon" warning would send the operator on a
// pointless restart. This is why the daemon fix in a release only takes
// effect after `cc-clip update` reinstalls the service; replacing the binary
// in place leaves the old daemon running with no visible sign until now.
func daemonVersionNotice(runningVersion, ownVersion string) string {
	running := normalizeVersion(runningVersion)
	own := normalizeVersion(ownVersion)
	if running == "" || own == "" || running == own {
		return ""
	}
	return fmt.Sprintf(
		"daemon:  WARNING: running daemon is %s but this binary is %s; restart it with 'cc-clip update' (or 'cc-clip service uninstall && cc-clip service install')",
		running, own,
	)
}
