package main

import (
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/tunnel"
)

func joinReport(lines []string) string {
	return strings.Join(lines, "\n")
}

// TestTunnelVerificationReportNeverClaimsSuccessWithoutDaemon is the guard for
// the reason this report exists: connect used a TCP-only probe, so a stale
// sshd still holding the forwarded port made it print "tunnel verified" for a
// tunnel that could not carry a byte. Only a daemon that answered may produce
// a success line.
func TestTunnelVerificationReportNeverClaimsSuccessWithoutDaemon(t *testing.T) {
	notHealthy := []tunnel.RemoteTunnelState{
		tunnel.RemoteTunnelDown,
		tunnel.RemoteTunnelStale,
		tunnel.RemoteTunnelUnverified,
		tunnel.RemoteTunnelUnknown,
	}
	for _, state := range notHealthy {
		got := joinReport(tunnelVerificationReport(state, 18339, "venus"))
		if strings.Contains(got, "tunnel verified") {
			t.Fatalf("state %q must not report a verified tunnel, got:\n%s", state, got)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("state %q produced no operator guidance", state)
		}
	}

	ok := joinReport(tunnelVerificationReport(tunnel.RemoteTunnelOK, 18339, "venus"))
	if !strings.Contains(ok, "tunnel verified") {
		t.Fatalf("healthy state must report a verified tunnel, got:\n%s", ok)
	}
}

// TestTunnelVerificationReportStaleNamesBothCauses pins the guidance for the
// state operators actually have to act on. Sending them to check RemoteForward
// when the real cause is a dead local daemon (or vice versa) is the failure
// mode this replaces.
func TestTunnelVerificationReportStaleNamesBothCauses(t *testing.T) {
	got := strings.ToLower(joinReport(tunnelVerificationReport(tunnel.RemoteTunnelStale, 18339, "venus")))

	for _, want := range []string{"stale", "cc-clip serve", "lsof"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stale guidance must mention %q, got:\n%s", want, got)
		}
	}
	// The RemoteForward advice belongs to the "nothing listening" case; on a
	// stale port it sends the operator down the wrong path.
	if strings.Contains(got, "remoteforward") {
		t.Fatalf("stale guidance must not blame RemoteForward, got:\n%s", got)
	}
}

// TestTunnelVerificationReportDownKeepsRemoteForwardGuidance preserves the
// pre-existing, correct advice for the common "no interactive SSH session is
// open" case.
func TestTunnelVerificationReportDownKeepsRemoteForwardGuidance(t *testing.T) {
	got := joinReport(tunnelVerificationReport(tunnel.RemoteTunnelDown, 18339, "venus"))

	if !strings.Contains(got, "RemoteForward 18339 127.0.0.1:18339") {
		t.Fatalf("down guidance must keep the ssh_config snippet, got:\n%s", got)
	}
	if !strings.Contains(got, "ssh -R 18339:127.0.0.1:18339 venus") {
		t.Fatalf("down guidance must keep the ssh -R example for the host, got:\n%s", got)
	}
}

func TestTunnelVerificationReportUnknownAdmitsItDidNotRun(t *testing.T) {
	got := strings.ToLower(joinReport(tunnelVerificationReport(tunnel.RemoteTunnelUnknown, 18339, "venus")))

	if !strings.Contains(got, "did not complete") {
		t.Fatalf("unknown guidance must say the check did not complete, got:\n%s", got)
	}
}
