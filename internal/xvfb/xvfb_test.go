package xvfb

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Unit tests (always run, no Xvfb needed)
// ---------------------------------------------------------------------------

func TestParseDisplayFile(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain number", input: "99", want: "99"},
		{name: "with colon prefix", input: ":99", want: "99"},
		{name: "with trailing newline", input: "99\n", want: "99"},
		{name: "colon and newline", input: ":99\n", want: "99"},
		{name: "zero display", input: "0", want: "0"},
		{name: "large number", input: "1024", want: "1024"},
		{name: "empty string", input: "", wantErr: true},
		{name: "whitespace only", input: "   \n", wantErr: true},
		{name: "non-numeric", input: "abc", wantErr: true},
		{name: "mixed", input: "99abc", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
		{name: "only colon", input: ":", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDisplayFile(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDisplayFile(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDisplayFile(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDisplayFile(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSocketPath(t *testing.T) {
	tests := []struct {
		display string
		want    string
	}{
		{"99", "/tmp/.X11-unix/X99"},
		{"0", "/tmp/.X11-unix/X0"},
		{"1024", "/tmp/.X11-unix/X1024"},
	}

	for _, tt := range tests {
		t.Run("display_"+tt.display, func(t *testing.T) {
			got := SocketPath(tt.display)
			if got != tt.want {
				t.Fatalf("SocketPath(%q) = %q, want %q", tt.display, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Mock executor for unit testing CheckAvailable, IsHealthy, etc.
// ---------------------------------------------------------------------------

type mockExecutor struct {
	responses map[string]mockResponse
	execLog   []string
}

type mockResponse struct {
	output string
	err    error
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		responses: make(map[string]mockResponse),
	}
}

func (m *mockExecutor) on(cmdPrefix string, output string, err error) {
	m.responses[cmdPrefix] = mockResponse{output: output, err: err}
}

func (m *mockExecutor) Exec(cmd string) (string, error) {
	m.execLog = append(m.execLog, cmd)

	// Try exact match first
	if resp, ok := m.responses[cmd]; ok {
		return resp.output, resp.err
	}

	// Try prefix match
	for prefix, resp := range m.responses {
		if strings.HasPrefix(cmd, prefix) {
			return resp.output, resp.err
		}
	}

	// Default: command not found / failed
	return "", fmt.Errorf("mock: unhandled command: %s", cmd)
}

func TestCheckAvailable_Found(t *testing.T) {
	m := newMockExecutor()
	m.on("which Xvfb", "/usr/bin/Xvfb", nil)

	err := CheckAvailable(m)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCheckAvailable_NotFound(t *testing.T) {
	m := newMockExecutor()
	m.on("which Xvfb", "", fmt.Errorf("exit status 1"))

	err := CheckAvailable(m)
	if err == nil {
		t.Fatal("expected error when xvfb not found")
	}
	if !strings.Contains(err.Error(), "xvfb not found") {
		t.Fatalf("error should mention xvfb not found, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo apt install xvfb") {
		t.Fatalf("error should contain Debian install hint, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo dnf install") {
		t.Fatalf("error should contain Fedora install hint, got: %v", err)
	}
}

func TestIsHealthy_AllGood(t *testing.T) {
	m := newMockExecutor()
	stateDir := "/tmp/test-xvfb"

	m.on("cat "+stateDir+"/xvfb.pid", "12345", nil)
	m.on("cat "+stateDir+"/display", "42", nil)
	m.on("kill -0 12345", "", nil)
	m.on("test -S /tmp/.X11-unix/X42", "", nil)

	state, healthy := IsHealthy(m, stateDir)
	if !healthy {
		t.Fatal("expected healthy")
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Display != "42" {
		t.Fatalf("expected display 42, got %s", state.Display)
	}
	if state.PID != 12345 {
		t.Fatalf("expected PID 12345, got %d", state.PID)
	}
}

func TestIsHealthy_NoPidFile(t *testing.T) {
	m := newMockExecutor()
	stateDir := "/tmp/test-xvfb"

	m.on("cat "+stateDir+"/xvfb.pid", "", fmt.Errorf("no such file"))

	_, healthy := IsHealthy(m, stateDir)
	if healthy {
		t.Fatal("expected not healthy when PID file missing")
	}
}

func TestIsHealthy_ProcessDead(t *testing.T) {
	m := newMockExecutor()
	stateDir := "/tmp/test-xvfb"

	m.on("cat "+stateDir+"/xvfb.pid", "12345", nil)
	m.on("cat "+stateDir+"/display", "42", nil)
	m.on("kill -0 12345", "", fmt.Errorf("no such process"))

	_, healthy := IsHealthy(m, stateDir)
	if healthy {
		t.Fatal("expected not healthy when process is dead")
	}
}

func TestIsHealthy_NoSocket(t *testing.T) {
	m := newMockExecutor()
	stateDir := "/tmp/test-xvfb"

	m.on("cat "+stateDir+"/xvfb.pid", "12345", nil)
	m.on("cat "+stateDir+"/display", "42", nil)
	m.on("kill -0 12345", "", nil)
	m.on("test -S /tmp/.X11-unix/X42", "", fmt.Errorf("not a socket"))

	_, healthy := IsHealthy(m, stateDir)
	if healthy {
		t.Fatal("expected not healthy when socket missing")
	}
}

func TestCleanStale(t *testing.T) {
	m := newMockExecutor()
	stateDir := "/tmp/test-xvfb"

	m.on("rm -f", "", nil)

	err := CleanStale(m, stateDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the rm command was issued
	found := false
	for _, cmd := range m.execLog {
		if strings.Contains(cmd, "rm -f") &&
			strings.Contains(cmd, "xvfb.pid") &&
			strings.Contains(cmd, "display") &&
			strings.Contains(cmd, "xvfb.log") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rm -f command for state files, got: %v", m.execLog)
	}
}

// retryExecutor lets a command return failure for the first N calls, then
// succeed. Used to drive the socket-wait retry loop in StartRemote.
type retryExecutor struct {
	*mockExecutor
	failPrefix string
	failTimes  int
	failCount  int
}

func (r *retryExecutor) Exec(cmd string) (string, error) {
	if r.failPrefix != "" && strings.HasPrefix(cmd, r.failPrefix) {
		if r.failCount < r.failTimes {
			r.failCount++
			r.execLog = append(r.execLog, cmd)
			return "", fmt.Errorf("not ready yet")
		}
	}
	return r.mockExecutor.Exec(cmd)
}

func TestStartRemote_SocketWaitDoesNotSleepViaExecutor(t *testing.T) {
	stateDir := "/tmp/test-xvfb"
	socketPath := SocketPath("42")

	base := newMockExecutor()
	base.on("which Xvfb", "/usr/bin/Xvfb", nil)
	// Force IsHealthy to report not-healthy so StartRemote takes the start path.
	base.on("cat "+stateDir+"/xvfb.pid 2>/dev/null", "", fmt.Errorf("no pid"))
	base.on("rm -f", "", nil)
	// The start script returns the chosen display number.
	base.on("mkdir -p", "42", nil)
	// PID read (non-2>/dev/null variant used after start).
	base.on("cat "+stateDir+"/xvfb.pid", "12345", nil)
	// Socket check eventually succeeds (after the retry loop iterates).
	base.on("test -S "+socketPath, "", nil)

	m := &retryExecutor{
		mockExecutor: base,
		failPrefix:   "test -S " + socketPath,
		failTimes:    2, // fail twice, forcing the wait loop to spin
	}

	state, err := StartRemote(m, stateDir)
	if err != nil {
		t.Fatalf("StartRemote failed: %v", err)
	}
	if state.Display != "42" || state.PID != 12345 {
		t.Fatalf("unexpected state: %+v", state)
	}

	// The wait between socket-check retries must be a local time.Sleep,
	// NOT a remote SSH round-trip. A standalone "sleep ..." command issued via
	// the executor is the regression we guard against. (The "sleep 0.2" inside
	// the embedded start-script shell loop is legitimate and not standalone.)
	for _, cmd := range m.execLog {
		if strings.HasPrefix(strings.TrimSpace(cmd), "sleep ") && !strings.Contains(cmd, "\n") {
			t.Fatalf("StartRemote sent a standalone remote sleep command to the executor: %q", cmd)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration tests (require Xvfb)
// ---------------------------------------------------------------------------

func requireXvfb(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb not available, skipping X11 test")
	}
}

// localExecutor runs commands locally via bash, simulating a remote executor.
type localExecutor struct{}

func (l *localExecutor) Exec(cmd string) (string, error) {
	out, err := exec.Command("bash", "-c", cmd).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestStartRemote_AndStop(t *testing.T) {
	requireXvfb(t)

	session := &localExecutor{}
	stateDir := t.TempDir()

	// Start Xvfb
	state, err := StartRemote(session, stateDir)
	if err != nil {
		t.Fatalf("StartRemote failed: %v", err)
	}

	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Display == "" {
		t.Fatal("expected non-empty display")
	}
	if state.PID <= 0 {
		t.Fatalf("expected positive PID, got %d", state.PID)
	}

	// Verify display is a valid number
	_, err = strconv.Atoi(state.Display)
	if err != nil {
		t.Fatalf("display %q is not a valid number: %v", state.Display, err)
	}

	// Verify socket exists
	socketPath := SocketPath(state.Display)
	checkOut, err := session.Exec(fmt.Sprintf("test -S %s && echo yes", socketPath))
	if err != nil || !strings.Contains(checkOut, "yes") {
		t.Fatalf("X11 socket %s does not exist after start", socketPath)
	}

	// Verify PID is alive
	_, err = session.Exec(fmt.Sprintf("kill -0 %d", state.PID))
	if err != nil {
		t.Fatalf("Xvfb process %d is not alive after start", state.PID)
	}

	// Stop Xvfb
	err = StopRemote(session, stateDir)
	if err != nil {
		t.Fatalf("StopRemote failed: %v", err)
	}

	// Verify process is gone
	_, err = session.Exec(fmt.Sprintf("kill -0 %d 2>/dev/null", state.PID))
	if err == nil {
		t.Fatalf("Xvfb process %d should be dead after stop", state.PID)
	}

	// Verify state files are cleaned up
	pidOut, _ := session.Exec(fmt.Sprintf("cat %s/xvfb.pid 2>/dev/null", stateDir))
	if strings.TrimSpace(pidOut) != "" {
		t.Fatal("PID file should be removed after stop")
	}

	displayOut, _ := session.Exec(fmt.Sprintf("cat %s/display 2>/dev/null", stateDir))
	if strings.TrimSpace(displayOut) != "" {
		t.Fatal("display file should be removed after stop")
	}
}

func TestStartRemote_ReuseHealthy(t *testing.T) {
	requireXvfb(t)

	session := &localExecutor{}
	stateDir := t.TempDir()

	// Start first instance
	state1, err := StartRemote(session, stateDir)
	if err != nil {
		t.Fatalf("first StartRemote failed: %v", err)
	}

	// Start again: should reuse existing healthy instance
	state2, err := StartRemote(session, stateDir)
	if err != nil {
		t.Fatalf("second StartRemote failed: %v", err)
	}

	if state1.PID != state2.PID {
		t.Fatalf("expected same PID on reuse: first=%d, second=%d", state1.PID, state2.PID)
	}
	if state1.Display != state2.Display {
		t.Fatalf("expected same display on reuse: first=%s, second=%s", state1.Display, state2.Display)
	}

	// Cleanup
	if err := StopRemote(session, stateDir); err != nil {
		t.Fatalf("StopRemote failed: %v", err)
	}
}

func TestStartRemote_RecoverStale(t *testing.T) {
	requireXvfb(t)

	session := &localExecutor{}
	stateDir := t.TempDir()

	// Start first instance
	state1, err := StartRemote(session, stateDir)
	if err != nil {
		t.Fatalf("first StartRemote failed: %v", err)
	}

	oldPID := state1.PID

	// Kill the process manually (simulating crash)
	session.Exec(fmt.Sprintf("kill -9 %d 2>/dev/null", oldPID))
	// Wait briefly for process to die
	session.Exec("sleep 0.3")

	// Start again: should detect stale state and start new instance
	state2, err := StartRemote(session, stateDir)
	if err != nil {
		t.Fatalf("second StartRemote (recovery) failed: %v", err)
	}

	if state2.PID == oldPID {
		t.Fatalf("expected new PID after recovery, got same PID %d", oldPID)
	}
	if state2.PID <= 0 {
		t.Fatalf("expected positive PID, got %d", state2.PID)
	}

	// Verify the new instance is healthy
	_, healthy := IsHealthy(session, stateDir)
	if !healthy {
		t.Fatal("new instance should be healthy after recovery")
	}

	// Cleanup
	if err := StopRemote(session, stateDir); err != nil {
		t.Fatalf("StopRemote failed: %v", err)
	}
}
