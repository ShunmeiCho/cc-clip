package main

import (
	"fmt"

	"github.com/shunmei/cc-clip/internal/tunnel"
)

// tunnelVerificationReport renders the operator-facing lines for a remote
// tunnel probe outcome. It is kept separate from connectVerifyTunnel so the
// guidance can be tested without an SSH session.
//
// Each state gets its own remedy. Collapsing "nothing is listening" and "the
// port is held by something that is not a cc-clip daemon" into one message is
// what previously sent operators to fix RemoteForward while the real cause was
// a stale sshd or a stopped local daemon.
func tunnelVerificationReport(state tunnel.RemoteTunnelState, port int, host string) []string {
	switch state {
	case tunnel.RemoteTunnelOK:
		return []string{"      tunnel verified (cc-clip daemon answered through the existing SSH session)"}

	case tunnel.RemoteTunnelStale:
		return []string{
			fmt.Sprintf("      WARNING: port %d is held on the remote, but no cc-clip daemon answered.", port),
			"      The port being open does NOT mean the tunnel works. Two known causes:",
			"        1. A stale sshd from a previous SSH session still owns the forward.",
			fmt.Sprintf("           On the remote, find and end it: lsof -ti tcp:%d", port),
			"        2. The forward is live but the local daemon is not running.",
			"           On this machine, start it: cc-clip serve",
			fmt.Sprintf("      Then re-run: cc-clip doctor --host %s", host),
		}

	case tunnel.RemoteTunnelUnverified:
		return []string{
			fmt.Sprintf("      port %d is reachable from the remote, but liveness could not be confirmed.", port),
			"      The remote has no curl, so the daemon could not be asked to identify itself.",
			"      Install curl on the remote to get a real verification.",
		}

	case tunnel.RemoteTunnelUnknown:
		return []string{
			"      tunnel check did not complete (the remote probe returned unrecognized output).",
			fmt.Sprintf("      Run 'cc-clip doctor --host %s' for a full diagnosis.", host),
		}

	default: // tunnel.RemoteTunnelDown
		return []string{
			"      tunnel not detected (this is normal if no interactive SSH session is open)",
			"      The tunnel is provided by your SSH connection, not by 'cc-clip connect'.",
			"      Ensure your SSH session includes RemoteForward:",
			fmt.Sprintf("        ssh -R %d:127.0.0.1:%d %s", port, port, host),
			"",
			"      Or add to ~/.ssh/config:",
			fmt.Sprintf("        Host %s", host),
			fmt.Sprintf("            RemoteForward %d 127.0.0.1:%d", port, port),
		}
	}
}
