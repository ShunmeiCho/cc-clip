package tunnel

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/shim"
)

func TestRemoteHealthProbeCommandTargetsLoopbackHealth(t *testing.T) {
	cmd := RemoteHealthProbeCommand(18339)

	// The probe must stay pinned to the remote's loopback interface: the
	// forwarded port is bound there and nowhere else.
	if !strings.Contains(cmd, "127.0.0.1/18339") {
		t.Fatalf("probe command must TCP-check 127.0.0.1/18339, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "http://127.0.0.1:18339/health") {
		t.Fatalf("probe command must GET the loopback /health endpoint, got:\n%s", cmd)
	}
	// Every branch has to emit a marker, otherwise the classifier cannot
	// tell "the probe ran and found nothing" from "the probe never ran".
	for _, marker := range []string{probeMarkerOK, probeMarkerDown, probeMarkerStale, probeMarkerUnverified} {
		if !strings.Contains(cmd, marker) {
			t.Fatalf("probe command must be able to emit %q, got:\n%s", marker, cmd)
		}
	}
}

func TestClassifyRemoteProbeOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want RemoteTunnelState
	}{
		{"healthy daemon", probeMarkerOK, RemoteTunnelOK},
		{"nothing listening", probeMarkerDown, RemoteTunnelDown},
		{"listener without cc-clip daemon", probeMarkerStale, RemoteTunnelStale},
		{"no http client on remote", probeMarkerUnverified, RemoteTunnelUnverified},
		{"marker with surrounding noise", "warning: blah\n" + probeMarkerOK + "\n", RemoteTunnelOK},
		{"empty output", "", RemoteTunnelUnknown},
		{"unrelated output", "bash: line 1: syntax error", RemoteTunnelUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyRemoteProbeOutput(tc.out); got != tc.want {
				t.Fatalf("ClassifyRemoteProbeOutput(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// TestRemoteTunnelStaleIsNotHealthy is the regression guard for the bug this
// probe exists to fix: a stale sshd from a previous session still owns the
// LISTEN socket, so a TCP-only check reports success for a tunnel that cannot
// carry a single byte to the daemon. Stale must never be treated as healthy.
func TestRemoteTunnelStaleIsNotHealthy(t *testing.T) {
	for _, state := range []RemoteTunnelState{RemoteTunnelStale, RemoteTunnelDown, RemoteTunnelUnknown, RemoteTunnelUnverified} {
		if state.Healthy() {
			t.Fatalf("state %q must not report Healthy()", state)
		}
	}
	if !RemoteTunnelOK.Healthy() {
		t.Fatal("RemoteTunnelOK must report Healthy()")
	}
}

func TestRemoteTunnelStateSummaryIsDistinctPerState(t *testing.T) {
	states := []RemoteTunnelState{
		RemoteTunnelOK,
		RemoteTunnelDown,
		RemoteTunnelStale,
		RemoteTunnelUnverified,
		RemoteTunnelUnknown,
	}

	seen := make(map[string]RemoteTunnelState, len(states))
	for _, state := range states {
		summary := state.Summary(18339)
		if summary == "" {
			t.Fatalf("state %q has an empty summary", state)
		}
		if prev, dup := seen[summary]; dup {
			t.Fatalf("states %q and %q share the summary %q", prev, state, summary)
		}
		seen[summary] = state
	}

	// The stale summary is the one an operator acts on, so it must name both
	// causes rather than sending them to look at only one.
	stale := RemoteTunnelStale.Summary(18339)
	for _, want := range []string{"stale", "daemon"} {
		if !strings.Contains(strings.ToLower(stale), want) {
			t.Fatalf("stale summary must mention %q, got %q", want, stale)
		}
	}
}

// requireProbeShellTools skips when the local machine cannot stand in for a
// remote: the probe needs bash for /dev/tcp and curl for the health request.
func requireProbeShellTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"bash", "curl", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available; skipping shell round-trip", bin)
		}
	}
}

// runProbeThroughLoginShell executes the probe exactly the way a remote does:
// shim.WrapRemoteShell produces one command string, and the login shell parses
// it. This is what catches single-quote escaping mistakes — the probe script
// is full of quotes, and a broken one degrades silently into "unknown".
func runProbeThroughLoginShell(t *testing.T, port int) string {
	t.Helper()
	wrapped := shim.WrapRemoteShell(RemoteHealthProbeCommand(port))
	out, err := exec.Command("sh", "-c", wrapped).CombinedOutput()
	if err != nil {
		t.Fatalf("probe command failed to execute: %v\noutput: %s\nwrapped: %s", err, out, wrapped)
	}
	return string(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind loopback: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestRemoteProbeShellRoundTripHealthyDaemon(t *testing.T) {
	requireProbeShellTools(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"service":"cc-clip","status":"ok"}`)
	}))
	defer ts.Close()

	port := ts.Listener.Addr().(*net.TCPAddr).Port
	got := ClassifyRemoteProbeOutput(runProbeThroughLoginShell(t, port))
	if got != RemoteTunnelOK {
		t.Fatalf("healthy daemon classified as %q, want %q", got, RemoteTunnelOK)
	}
}

// TestRemoteProbeShellRoundTripStaleListener reproduces the actual bug: a
// listener that completes the TCP handshake and then goes silent, exactly as a
// stale sshd forward does. The old /dev/tcp-only probe called this healthy.
func TestRemoteProbeShellRoundTripStaleListener(t *testing.T) {
	requireProbeShellTools(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind loopback: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close() // accept, then drop — no HTTP is ever spoken
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	got := ClassifyRemoteProbeOutput(runProbeThroughLoginShell(t, port))
	if got != RemoteTunnelStale {
		t.Fatalf("stale listener classified as %q, want %q", got, RemoteTunnelStale)
	}
}

func TestRemoteProbeShellRoundTripNothingListening(t *testing.T) {
	requireProbeShellTools(t)

	port := freePort(t)
	got := ClassifyRemoteProbeOutput(runProbeThroughLoginShell(t, port))
	if got != RemoteTunnelDown {
		t.Fatalf("closed port classified as %q, want %q", got, RemoteTunnelDown)
	}
}

// TestRemoteProbeShellRoundTripRejectsNonCcClipService guards the identity
// check: something else listening on the port (a different service, another
// user's server) must not be accepted as a cc-clip daemon.
func TestRemoteProbeShellRoundTripRejectsNonCcClipService(t *testing.T) {
	requireProbeShellTools(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"service":"something-else","status":"ok"}`)
	}))
	defer ts.Close()

	port := ts.Listener.Addr().(*net.TCPAddr).Port
	got := ClassifyRemoteProbeOutput(runProbeThroughLoginShell(t, port))
	if got != RemoteTunnelStale {
		t.Fatalf("foreign service classified as %q, want %q", got, RemoteTunnelStale)
	}
}
