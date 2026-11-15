//go:build !windows

package tunnelmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/tunnel"
)

// newTestSupervisor wires a supervisor to a fake ssh binary and a temp state
// store, with fast intervals so the whole lifecycle fits in milliseconds.
func newTestSupervisor(t *testing.T, port int) (*Supervisor, *Store) {
	t.Helper()
	fake := writeFakeSSH(t)
	storePath := filepath.Join(t.TempDir(), "tunnel-state.json")
	store := NewStoreAt(storePath)
	spec := Spec{Host: "fake-host", Port: port, ExpectedInstanceID: "instance-123"}
	b := &Backend{
		Spec:       spec,
		SSHBinary:  fake,
		ControlDir: t.TempDir(),
	}
	sup := NewSupervisor(spec, store, b)
	sup.ProbeInterval = 30 * time.Millisecond
	sup.ProbeTimeout = 5 * time.Second
	sup.StartBackoffBase = 5 * time.Millisecond
	sup.StartBackoffMax = 20 * time.Millisecond
	sup.StartingGate = 50 * time.Millisecond
	sup.StableWindow = 200 * time.Millisecond
	sup.StopGrace = 2 * time.Second
	sup.ProbeLocal = func(string, time.Duration) error { return nil }
	sup.ReadLocalToken = func() (string, error) { return "token", nil }
	sup.FetchLocalIdentity = func(string, string, time.Duration) (tunnel.IdentityInfo, error) {
		return tunnel.IdentityInfo{Service: "cc-clip", Status: "ok", ProtocolVersion: 1, InstanceID: "instance-123"}, nil
	}
	return sup, store
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func runSupervisor(t *testing.T, sup *Supervisor) (cancel context.CancelFunc, done chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	return cancel, done
}

func TestSupervisorHealthyPathAndCleanStop(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")

	sup, store := newTestSupervisor(t, 18399)
	cancel, done := runSupervisor(t, sup)

	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateHealthy }, "healthy state")
	waitUntil(t, 5*time.Second, func() bool {
		rec, err := store.Load()
		return err == nil && rec.Runtime.State == StateHealthy && rec.Runtime.RemoteState == tunnel.RemoteTunnelOK
	}, "persisted healthy state")
	rec, err := store.Load()
	if err != nil {
		t.Fatalf("load state during run: %v", err)
	}
	if rec.SchemaVersion != schemaVersion || rec.Spec.Host != "fake-host" || rec.Spec.Port != 18399 {
		t.Fatalf("persisted record = %+v", rec)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if sup.State() != StateStopped {
		t.Fatalf("state after cancel = %q, want stopped", sup.State())
	}
	if sup.Backend.Running() {
		t.Fatal("child must be terminated after shutdown")
	}
	final, err := store.Load()
	if err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if final.Runtime.State != StateStopped {
		t.Fatalf("final persisted state = %q, want stopped", final.Runtime.State)
	}
}

func TestSupervisorHealthyChildExitWakesBeforeProbeInterval(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "die")
	t.Setenv("FAKE_SSH_UPTIME", "0.25")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")

	sup, store := newTestSupervisor(t, 18399)
	sup.ProbeInterval = 10 * time.Second
	sup.StartBackoffBase = 500 * time.Millisecond
	sup.StartBackoffMax = 500 * time.Millisecond
	sup.StartingGate = 50 * time.Millisecond
	sup.StableWindow = 5 * time.Second

	cancel, done := runSupervisor(t, sup)
	defer func() {
		cancel()
		<-done
	}()

	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateHealthy }, "healthy before child exit")
	waitUntil(t, 2*time.Second, func() bool {
		rec, err := store.Load()
		return err == nil && rec.Runtime.State == StateReconnecting
	}, "persisted reconnecting state immediately after child exit")
}

// TestSupervisorRemoteNotOKNeverHealthy covers the core fail-closed rule:
// only a remote probe of `ok` may produce StateHealthy. Every other probe
// outcome — including unparseable output — must surface as its own non-healthy
// state with the raw classification preserved.
func TestSupervisorRemoteNotOKNeverHealthy(t *testing.T) {
	cases := []struct {
		name       string
		out        string
		wantRemote tunnel.RemoteTunnelState
		wantState  State
	}{
		{"stale", "cc-clip-probe:stale\n", tunnel.RemoteTunnelStale, StateRemoteStale},
		{"down", "cc-clip-probe:down\n", tunnel.RemoteTunnelDown, StateRemoteDown},
		{"unverified", "cc-clip-probe:unverified\n", tunnel.RemoteTunnelUnverified, StateRemoteUnverified},
		{"unrecognized output", "some motd noise, no marker\n", tunnel.RemoteTunnelUnknown, StateRemoteUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAKE_SSH_MODE", "master")
			t.Setenv("FAKE_SSH_PROBE_OUT", tc.out)

			// Nothing listens on the port locally, so the stale annotation
			// path runs against a dead daemon.
			sup, store := newTestSupervisor(t, 18399)
			cancel, done := runSupervisor(t, sup)

			waitUntil(t, 5*time.Second, func() bool { return sup.State() == tc.wantState }, "independent remote state")
			if sup.State().Healthy() {
				t.Fatal("failed remote state must never be healthy")
			}
			if got := sup.RemoteState(); got != tc.wantRemote {
				t.Fatalf("remote state = %q, want %q", got, tc.wantRemote)
			}
			waitUntil(t, 5*time.Second, func() bool {
				rec, err := store.Load()
				return err == nil && rec.Runtime.State == tc.wantState && rec.Runtime.RemoteState == tc.wantRemote
			}, "persisted remote state")
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return after cancel")
			}
		})
	}
}

func TestSupervisorRequiresAuthenticatedExpectedIdentity(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want State
	}{
		{"helper missing", "cc-clip-identity:helper-missing\n", StateIdentityHelperMissing},
		{"endpoint unavailable", "cc-clip-identity:endpoint-unavailable\n", StateIdentityEndpointUnavailable},
		{"token rejected", "cc-clip-identity:token-invalid\n", StateRemoteTokenInvalid},
		{"unknown identity", "cc-clip-identity:unknown\n", StateRemoteUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAKE_SSH_MODE", "master")
			t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
			t.Setenv("FAKE_SSH_IDENTITY_OUT", tc.out)
			sup, _ := newTestSupervisor(t, 18399)
			cancel, done := runSupervisor(t, sup)
			waitUntil(t, 5*time.Second, func() bool { return sup.State() == tc.want }, string(tc.want))
			if sup.State().Healthy() {
				t.Fatalf("identity state %q must not be healthy", tc.want)
			}
			cancel()
			<-done
		})
	}

	t.Run("different instance has dedicated state", func(t *testing.T) {
		t.Setenv("FAKE_SSH_MODE", "master")
		t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
		t.Setenv("FAKE_SSH_IDENTITY_OUT", `cc-clip-identity:ok:{"service":"cc-clip","status":"ok","protocol_version":1,"instance_id":"other-instance"}`)
		sup, store := newTestSupervisor(t, 18399)
		_, done := runSupervisor(t, sup)
		if err := <-done; err == nil || !strings.Contains(err.Error(), "identity mismatch") {
			t.Fatalf("Run error = %v, want identity mismatch", err)
		}
		rec, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if rec.Runtime.State != StateIdentityMismatch {
			t.Fatalf("persisted state = %q, want identity-mismatch", rec.Runtime.State)
		}
	})
}

func TestSupervisorLocalIdentityFailuresRemainDistinct(t *testing.T) {
	sup, _ := newTestSupervisor(t, 18399)
	sup.FetchLocalIdentity = func(string, string, time.Duration) (tunnel.IdentityInfo, error) {
		return tunnel.IdentityInfo{Service: "cc-clip", Status: "ok", ProtocolVersion: 1, InstanceID: "other-instance"}, nil
	}
	if ready, state, reason := sup.localReady(); ready || state != StateIdentityMismatch {
		t.Fatalf("local identity mismatch = ready %v, state %q; want identity-mismatch", ready, state)
	} else if !strings.Contains(reason, "on your local machine") || !strings.Contains(reason, "not on the remote host") {
		// #182: run on the remote host, this is the error users see.
		t.Fatalf("local identity mismatch must say where to run tunnel run, got %q", reason)
	}

	sup.FetchLocalIdentity = func(string, string, time.Duration) (tunnel.IdentityInfo, error) {
		return tunnel.IdentityInfo{}, tunnel.ErrIdentityEndpointUnavailable
	}
	if ready, state, _ := sup.localReady(); ready || state != StateIdentityEndpointUnavailable {
		t.Fatalf("local endpoint unavailable = ready %v, state %q; want identity-endpoint-unavailable", ready, state)
	}

	sup.ReadLocalToken = func() (string, error) { return "", errors.New("token file missing") }
	if ready, state, _ := sup.localReady(); ready || state != StateConfigError {
		t.Fatalf("local token read failure = ready %v, state %q; want config-error", ready, state)
	}
}

func TestSupervisorTokenInvalidKeepsMasterAndRecoversInPlace(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
	t.Setenv("FAKE_SSH_IDENTITY_OUT", "cc-clip-identity:token-invalid\n")
	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)

	sup, store := newTestSupervisor(t, 18399)
	var logMu sync.Mutex
	var logs []string
	sup.Logf = func(format string, args ...any) {
		logMu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		logMu.Unlock()
	}
	cancel, done := runSupervisor(t, sup)
	defer func() {
		cancel()
		<-done
	}()

	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateRemoteTokenInvalid }, "remote-token-invalid")
	controlPath := sup.Backend.ControlPath()
	if controlPath == "" || !sup.Backend.Running() || !sup.Backend.Forwarded() {
		t.Fatalf("token failure discarded tunnel: control=%q running=%v forwarded=%v", controlPath, sup.Backend.Running(), sup.Backend.Forwarded())
	}
	if got := fakeMasterStarts(countFile); got != 1 {
		t.Fatalf("master starts while token invalid = %d, want 1", got)
	}
	wantCommand := `cc-clip connect "fake-host" --token-only`
	var rec *Record
	waitUntil(t, 5*time.Second, func() bool {
		var err error
		rec, err = store.Load()
		return err == nil && rec.Runtime.State == StateRemoteTokenInvalid && strings.Contains(rec.Runtime.LastError, wantCommand)
	}, "persisted token recovery guidance")
	if !strings.Contains(rec.Runtime.LastError, wantCommand) {
		t.Fatalf("token recovery detail = %q, want command %q", rec.Runtime.LastError, wantCommand)
	}
	var joinedLogs string
	waitUntil(t, 5*time.Second, func() bool {
		logMu.Lock()
		joinedLogs = strings.Join(logs, "\n")
		logMu.Unlock()
		return strings.Contains(joinedLogs, wantCommand)
	}, "token recovery log")

	// Simulate an external `connect --token-only` repair. The next identity
	// probe must promote the same forward to healthy without respawning SSH.
	t.Setenv("FAKE_SSH_IDENTITY_OUT", `cc-clip-identity:ok:{"service":"cc-clip","status":"ok","protocol_version":1,"instance_id":"instance-123"}`)
	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateHealthy }, "healthy after token repair")
	if got := sup.Backend.ControlPath(); got != controlPath {
		t.Fatalf("control path changed during token repair: got %q, want %q", got, controlPath)
	}
	if got := fakeMasterStarts(countFile); got != 1 {
		t.Fatalf("master starts after token repair = %d, want 1", got)
	}
}

func fakeMasterStarts(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "x")
}

func TestSupervisorPortConflictKeepsHealthyMaster(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_FORWARD_MODE", "conflict")
	sup, _ := newTestSupervisor(t, 18399)
	sup.ConflictInterval = 30 * time.Millisecond
	cancel, done := runSupervisor(t, sup)
	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StatePortConflict }, "port-conflict")
	if !sup.Backend.Running() || sup.Backend.Forwarded() {
		t.Fatalf("conflict master state: running=%v forwarded=%v", sup.Backend.Running(), sup.Backend.Forwarded())
	}
	time.Sleep(50 * time.Millisecond)
	if sup.State() == StateCrashLoop {
		t.Fatal("port conflict must not enter crash-loop")
	}
	cancel()
	<-done
}

func TestSupervisorPortConflictWithFailedCheckRestartsMaster(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_FORWARD_MODE", "conflict")
	t.Setenv("FAKE_SSH_CHECK_MODE", "after-conflict")
	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)

	sup, _ := newTestSupervisor(t, 18399)
	sup.CrashLoopThreshold = 5
	cancel, done := runSupervisor(t, sup)
	waitUntil(t, 5*time.Second, func() bool {
		data, _ := os.ReadFile(countFile)
		return strings.Count(string(data), "x") >= 2
	}, "master restart after conflict check failure")
	if sup.State() == StatePortConflict {
		t.Fatal("a master that fails -O check must not be retained as port-conflict")
	}
	cancel()
	<-done
}

func TestSupervisorForwardFailuresOpenCrashLoop(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_FORWARD_MODE", "fail")
	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)

	sup, store := newTestSupervisor(t, 18399)
	sup.StartingGate = 5 * time.Second
	sup.StartBackoffBase = 5 * time.Millisecond
	sup.StartBackoffMax = 15 * time.Millisecond
	sup.CrashLoopThreshold = 3

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCrashLoopOpen) {
			t.Fatalf("Run error = %v, want crash-loop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after repeated forward failures")
	}

	startsAtOpen := fakeMasterStarts(countFile)
	if startsAtOpen != sup.CrashLoopThreshold {
		t.Fatalf("master starts before circuit opened = %d, want %d", startsAtOpen, sup.CrashLoopThreshold)
	}
	time.Sleep(50 * time.Millisecond)
	if got := fakeMasterStarts(countFile); got != startsAtOpen {
		t.Fatalf("supervisor restarted after forward crash-loop: starts changed from %d to %d", startsAtOpen, got)
	}

	final, err := store.Load()
	if err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if final.Runtime.ConsecutiveStartFailures != sup.CrashLoopThreshold {
		t.Fatalf("persisted failures = %d, want %d", final.Runtime.ConsecutiveStartFailures, sup.CrashLoopThreshold)
	}
	if final.Runtime.State != StateCrashLoop {
		t.Fatalf("persisted state = %q, want crash-loop", final.Runtime.State)
	}
	if strings.Contains(final.Runtime.LastError, "code -1") {
		t.Fatalf("forward failure must not be reported as a child exit: %q", final.Runtime.LastError)
	}
	if !strings.Contains(final.Runtime.LastError, "forward request failed") {
		t.Fatalf("forward failure detail = %q, want underlying error", final.Runtime.LastError)
	}
}

func TestSupervisorAuthRequiredUsesAttentionWait(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "authfail")
	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)
	sup, _ := newTestSupervisor(t, 18399)
	sup.AttentionInterval = 200 * time.Millisecond
	cancel, done := runSupervisor(t, sup)
	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateAuthRequired }, "auth-required")
	time.Sleep(50 * time.Millisecond)
	data, _ := os.ReadFile(countFile)
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("auth-required starts = %d, want 1 during attention wait", got)
	}
	cancel()
	<-done
}

func TestSupervisorLocalDaemonGatePreventsSSHStart(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)
	sup, _ := newTestSupervisor(t, 18399)
	sup.LocalRetryInterval = 20 * time.Millisecond
	sup.ProbeLocal = func(string, time.Duration) error { return tunnel.ErrDaemonNotAnswering }
	cancel, done := runSupervisor(t, sup)
	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateLocalDaemonDown }, "local-daemon-down")
	time.Sleep(50 * time.Millisecond)
	if data, _ := os.ReadFile(countFile); len(data) != 0 {
		t.Fatalf("SSH started while local daemon was down: %q", data)
	}
	cancel()
	<-done
}

func TestSupervisorHealthySnapshotStaysFresh(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
	sup, store := newTestSupervisor(t, 18399)
	cancel, done := runSupervisor(t, sup)
	defer func() { cancel(); <-done }()
	var first time.Time
	waitUntil(t, 5*time.Second, func() bool {
		rec, err := store.Load()
		if err != nil || rec.Runtime.State != StateHealthy {
			return false
		}
		first = rec.Runtime.UpdatedAt
		return true
	}, "first healthy snapshot")
	waitUntil(t, 5*time.Second, func() bool {
		rec, err := store.Load()
		return err == nil && rec.Runtime.State == StateHealthy && rec.Runtime.UpdatedAt.After(first)
	}, "refreshed healthy snapshot")
}

func TestSupervisorDoesNotPersistSSHHelperSecrets(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "secretfail")
	const secret = "test-proxy-secret-never-persist"
	t.Setenv("FAKE_SSH_SECRET", secret)
	sup, store := newTestSupervisor(t, 18399)
	sup.AttentionInterval = time.Minute
	var logs strings.Builder
	sup.Logf = func(format string, args ...any) { fmt.Fprintf(&logs, format, args...) }
	cancel, done := runSupervisor(t, sup)
	waitUntil(t, 5*time.Second, func() bool {
		rec, err := store.Load()
		return err == nil && rec.Runtime.State == StateAuthRequired
	}, "persisted authentication failure")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data)+logs.String(), secret) {
		t.Fatal("SSH helper secret leaked into runtime or log")
	}
	if !strings.Contains(string(data), "SSH authentication required") {
		t.Fatal("safe authentication diagnostic missing")
	}
}

func TestSupervisorSpecMismatchNamesDifferencesWithoutReset(t *testing.T) {
	sup, store := newTestSupervisor(t, 18340)
	record := validRecord()
	record.Spec.Host = sup.Spec.Host
	record.Spec.ExpectedInstanceID = "old-instance"
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	err = sup.Run(context.Background())
	for _, want := range []string{"persisted tunnel spec differs", "port: persisted=18339, requested=18340", `expected_instance_id: persisted="old-instance", requested="instance-123"`, "--reset"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in mismatch: %v", want, err)
		}
	}
	after, err := os.ReadFile(store.Path())
	if err != nil || string(before) != string(after) || sup.Backend.Running() {
		t.Fatal("spec mismatch changed runtime or started SSH")
	}
}

// TestSupervisorCrashLoopStopsRestarting verifies the circuit breaker: an
// rapidly dying generic SSH children open a persisted circuit breaker.
func TestSupervisorCrashLoopStopsRestarting(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "die")
	t.Setenv("FAKE_SSH_UPTIME", "0")

	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)

	sup, store := newTestSupervisor(t, 18399)
	sup.ProbeInterval = 20 * time.Millisecond
	sup.BootstrapTimeout = 500 * time.Millisecond
	sup.StartingGate = 5 * time.Second
	sup.StartBackoffBase = 5 * time.Millisecond
	sup.StartBackoffMax = 15 * time.Millisecond
	sup.CrashLoopThreshold = 3

	_, done := runSupervisor(t, sup)
	select {
	case err := <-done:
		if !errors.Is(err, ErrCrashLoopOpen) {
			t.Fatalf("Run error = %v, want crash-loop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop at crash-loop")
	}

	masterStarts := func() int {
		data, err := os.ReadFile(countFile)
		if err != nil {
			return 0
		}
		return strings.Count(string(data), "x")
	}
	startsAtOpen := masterStarts()
	if startsAtOpen == 0 || startsAtOpen > sup.CrashLoopThreshold {
		t.Fatalf("master starts before circuit opened = %d, want 1..%d", startsAtOpen, sup.CrashLoopThreshold)
	}
	if sup.State().Healthy() {
		t.Fatal("crash-loop must never be healthy")
	}

	time.Sleep(50 * time.Millisecond)
	if got := masterStarts(); got != startsAtOpen {
		t.Fatalf("supervisor kept restarting in crash-loop: starts changed from %d to %d", startsAtOpen, got)
	}

	final, err := store.Load()
	if err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if final.Runtime.ConsecutiveStartFailures != 3 {
		t.Fatalf("persisted failures = %d, want 3", final.Runtime.ConsecutiveStartFailures)
	}
	if final.Runtime.State != StateCrashLoop {
		t.Fatalf("persisted state = %q, want crash-loop", final.Runtime.State)
	}
	if !strings.Contains(final.Runtime.LastError, "ssh child exited with code 255") {
		t.Fatalf("real child exit detail = %q, want exit code", final.Runtime.LastError)
	}
	second, _ := newTestSupervisor(t, 18399)
	second.Store = store
	second.Spec = sup.Spec
	second.Backend.Spec = sup.Spec
	if err := second.Run(context.Background()); !errors.Is(err, ErrCrashLoopOpen) {
		t.Fatalf("restarted supervisor error = %v, want persisted crash-loop", err)
	}
}

// TestSupervisorStableExitResetsBackoff verifies the stability gate: a child
// that ran past StableWindow gets a fresh backoff sequence, so a connection
// that works and occasionally drops never accumulates into a crash-loop.
func TestSupervisorStableExitResetsBackoff(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "die")
	t.Setenv("FAKE_SSH_UPTIME", "0.25")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")

	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)

	sup, _ := newTestSupervisor(t, 18399)
	sup.ProbeInterval = 20 * time.Millisecond
	sup.ProbeTimeout = 5 * time.Second
	sup.StartBackoffBase = 5 * time.Millisecond
	sup.StartBackoffMax = 15 * time.Millisecond
	sup.StableWindow = 200 * time.Millisecond
	// The fake child lives ~250ms, past the 200ms stable window, then exits
	// 255. With the reset working, the supervisor must survive more master
	// starts than the crash-loop threshold without ever opening the breaker.
	sup.CrashLoopThreshold = 3

	masterStarts := func() int {
		data, err := os.ReadFile(countFile)
		if err != nil {
			return 0
		}
		return strings.Count(string(data), "x")
	}

	cancel, done := runSupervisor(t, sup)
	defer func() {
		cancel()
		<-done
	}()

	waitUntil(t, 8*time.Second, func() bool { return masterStarts() >= 5 }, "5 master starts")
	if sup.State() == StateCrashLoop {
		t.Fatal("hit crash-loop despite the stable window resetting backoff")
	}
	// And the tunnel comes back healthy after each drop.
	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateHealthy }, "healthy after drops")
}

func TestSupervisorExitPastStartingGateDoesNotOpenCrashLoop(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "die")
	t.Setenv("FAKE_SSH_UPTIME", "0.10")

	countFile := filepath.Join(t.TempDir(), "count")
	t.Setenv("FAKE_SSH_COUNT_FILE", countFile)

	sup, _ := newTestSupervisor(t, 18399)
	sup.StartingGate = 50 * time.Millisecond
	sup.StableWindow = 500 * time.Millisecond
	sup.CrashLoopThreshold = 2

	masterStarts := func() int {
		data, _ := os.ReadFile(countFile)
		return strings.Count(string(data), "x")
	}
	cancel, done := runSupervisor(t, sup)
	waitUntil(t, 5*time.Second, func() bool { return masterStarts() >= 4 }, "4 starts past starting gate")
	if sup.State() == StateCrashLoop {
		t.Fatal("a child that survives the starting gate must not count as a rapid-start crash")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestSupervisorBackoffDefaultsAndJitterBounds(t *testing.T) {
	sup, _ := newTestSupervisor(t, 18399)
	sup.StartBackoffBase = defaultStartBackoffBase
	sup.StartBackoffMax = defaultStartBackoffMax
	if sup.StartBackoffBase != time.Second || sup.StartBackoffMax != 5*time.Minute {
		t.Fatalf("backoff defaults = %s/%s, want 1s/5m", sup.StartBackoffBase, sup.StartBackoffMax)
	}

	for failures, nominal := range map[int]time.Duration{
		1:  time.Second,
		2:  2 * time.Second,
		3:  4 * time.Second,
		9:  4*time.Minute + 16*time.Second,
		10: 5 * time.Minute,
	} {
		sup.mu.Lock()
		sup.retryFailures = failures
		sup.mu.Unlock()
		lower := time.Duration(float64(nominal) * 0.8)
		upper := time.Duration(float64(nominal) * 1.2)
		if upper > sup.StartBackoffMax {
			upper = sup.StartBackoffMax
		}
		for i := 0; i < 50; i++ {
			if got := sup.backoff(); got < lower || got > upper {
				t.Fatalf("failures=%d backoff=%s, want within [%s,%s]", failures, got, lower, upper)
			}
		}
	}
}

// TestSupervisorFailsClosedOnCorruptStore verifies the store contract at the
// supervisor boundary: an unparseable or foreign-schema state file is a hard
// error before anything is spawned.
func TestSupervisorFailsClosedOnCorruptStore(t *testing.T) {
	for _, content := range []string{"{not json", `{"schema_version":2}`} {
		t.Run(content, func(t *testing.T) {
			fake := writeFakeSSH(t)
			t.Setenv("FAKE_SSH_MODE", "master")
			t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")

			storePath := filepath.Join(t.TempDir(), "tunnel-state.json")
			if err := os.WriteFile(storePath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			store := NewStoreAt(storePath)
			spec := Spec{Host: "fake-host", Port: 18399, ExpectedInstanceID: "instance-123"}
			b := &Backend{
				Spec:       spec,
				SSHBinary:  fake,
				ControlDir: t.TempDir(),
			}
			sup := NewSupervisor(spec, store, b)

			if err := sup.Run(context.Background()); err == nil {
				t.Fatal("Run must refuse to start on an unvalidatable state file")
			}
			if b.ControlPath() != "" {
				t.Fatal("no ssh child may be started when the state file fails validation")
			}
		})
	}
}
