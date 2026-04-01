package main

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

func TestStopLocalProcessDoesNotKillUnexpectedCommand(t *testing.T) {
	cmd := helperSleepProcess(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatalf("failed to write pid file: %v", err)
	}

	stopLocalProcess(pidFile, "Xvfb")
	time.Sleep(100 * time.Millisecond)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case err := <-waitDone:
		t.Fatalf("unexpected command should still be running, but exited early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after stale pid detection, got err=%v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-1] != "sleep-helper" {
		os.Exit(0)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func helperSleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()

	args := []string{"-test.run=TestHelperProcess", "--", "sleep-helper"}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return cmd
}

func TestReleaseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0.3.0", "0.3.0"},
		{"0.3.0-1-g99b1298", "0.3.0"},
		{"0.3.0-15-gabcdef0", "0.3.0"},
		{"1.0.0-rc1", "1.0.0-rc1"},              // pre-release tag, not git describe
		{"1.0.0-rc1-3-g1234567", "1.0.0-rc1"},   // git describe from pre-release tag
		{"0.3.0-beta-2-gabcdef0", "0.3.0-beta"}, // git describe from tag with dash
		{"dev", "dev"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := releaseVersion(tt.input)
			if got != tt.want {
				t.Errorf("releaseVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNotifyFromCodexParsesLastAssistantMessage(t *testing.T) {
	payload := `{"last-assistant-message":"Bridge implementation complete"}`
	msg, err := parseCodexNotifyPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Body != "Bridge implementation complete" {
		t.Fatalf("unexpected body %q", msg.Body)
	}
	if msg.Title != "Codex" {
		t.Fatalf("expected title %q, got %q", "Codex", msg.Title)
	}
}

func TestNotifyFromCodexRejectsInvalidJSON(t *testing.T) {
	_, err := parseCodexNotifyPayload(`{invalid`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNotifyFromCodexHandlesEmptyMessage(t *testing.T) {
	payload := `{"last-assistant-message":""}`
	msg, err := parseCodexNotifyPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Body != "" {
		t.Fatalf("expected empty body, got %q", msg.Body)
	}
}

func TestNotifyFromCodexHandlesMissingField(t *testing.T) {
	payload := `{"some-other-field":"value"}`
	msg, err := parseCodexNotifyPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Missing field => empty body
	if msg.Body != "" {
		t.Fatalf("expected empty body for missing field, got %q", msg.Body)
	}
}

func TestParseCodexNotifyPayloadReturnType(t *testing.T) {
	payload := `{"last-assistant-message":"test"}`
	msg, err := parseCodexNotifyPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the return type is GenericMessagePayload
	var _ daemon.GenericMessagePayload = msg
}

func TestRegisterNonceWithDaemonIntegration(t *testing.T) {
	tm := token.NewManager(time.Hour)
	sess, err := tm.Generate()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	store := session.NewStore(12 * time.Hour)
	srv := daemon.NewServer("127.0.0.1:0", &testClipboard{}, tm, store)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	port := extractPort(t, ts.URL)
	testNonce := "test-nonce-0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"

	if err := registerNonceWithDaemon(port, sess.Token, testNonce); err != nil {
		t.Fatalf("registerNonceWithDaemon failed: %v", err)
	}

	// Verify the nonce works by sending a health probe
	if err := runNotificationHealthProbe(port, testNonce); err != nil {
		t.Fatalf("health probe failed after nonce registration: %v", err)
	}
}

func TestRunNotificationHealthProbeFailsWithBadNonce(t *testing.T) {
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := daemon.NewServer("127.0.0.1:0", &testClipboard{}, tm, store)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	port := extractPort(t, ts.URL)

	err := runNotificationHealthProbe(port, "bad-nonce")
	if err == nil {
		t.Fatal("expected health probe to fail with unregistered nonce")
	}
}

func TestPostGenericNotificationDeliversExpectedPayload(t *testing.T) {
	tm := token.NewManager(time.Hour)
	_, err := tm.Generate()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	store := session.NewStore(12 * time.Hour)
	srv := daemon.NewServer("127.0.0.1:0", &testClipboard{}, tm, store)
	nonce := "test-notify-nonce-0123456789abcdef0123456789abcdef0123456789abcdef"
	srv.RegisterNotificationNonce(nonce)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	port := extractPort(t, ts.URL)

	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache", "cc-clip")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "notify.nonce"), []byte(nonce+"\n"), 0600); err != nil {
		t.Fatalf("failed to write nonce file: %v", err)
	}

	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("failed to set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", oldHome)
	}()

	msg := daemon.GenericMessagePayload{
		Title:   "Build complete",
		Body:    "All tests passed",
		Urgency: 1,
	}
	if err := postGenericNotification(port, msg); err != nil {
		t.Fatalf("postGenericNotification failed: %v", err)
	}

	select {
	case env := <-srv.NotifyChannel():
		if env.GenericMessage == nil {
			t.Fatal("expected GenericMessage payload")
		}
		if env.GenericMessage.Title != msg.Title {
			t.Fatalf("expected title %q, got %q", msg.Title, env.GenericMessage.Title)
		}
		if env.GenericMessage.Body != msg.Body {
			t.Fatalf("expected body %q, got %q", msg.Body, env.GenericMessage.Body)
		}
		if env.GenericMessage.Urgency != msg.Urgency {
			t.Fatalf("expected urgency %d, got %d", msg.Urgency, env.GenericMessage.Urgency)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected notification to be enqueued")
	}
}

func TestClaudeHookConfigJSONIncludesNotificationAndStop(t *testing.T) {
	cfg := claudeHookConfigJSON()
	if !strings.Contains(cfg, `"Notification"`) {
		t.Fatalf("expected Notification hook in config, got %q", cfg)
	}
	if !strings.Contains(cfg, `"Stop"`) {
		t.Fatalf("expected Stop hook in config, got %q", cfg)
	}
	if strings.Count(cfg, `"command": "cc-clip-hook"`) != 2 {
		t.Fatalf("expected hook command to appear twice, got %q", cfg)
	}
}

// testClipboard is a minimal mock for daemon.ClipboardReader.
type testClipboard struct{}

func (c *testClipboard) Type() (daemon.ClipboardInfo, error) {
	return daemon.ClipboardInfo{Type: daemon.ClipboardEmpty}, nil
}

func (c *testClipboard) ImageBytes() ([]byte, error) {
	return nil, nil
}

// extractPort extracts the port number from an httptest server URL.
func extractPort(t *testing.T, url string) int {
	t.Helper()
	// URL format: http://127.0.0.1:PORT
	parts := strings.Split(url, ":")
	if len(parts) < 3 {
		t.Fatalf("unexpected URL format: %s", url)
	}
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatalf("failed to parse port from URL %s: %v", url, err)
	}
	return port
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"0", true},
		{"123", true},
		{"", false},
		{"abc", false},
		{"12a", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isNumeric(tt.input)
			if got != tt.want {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
