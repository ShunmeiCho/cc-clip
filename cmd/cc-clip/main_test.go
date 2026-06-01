package main

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/shim"
	"github.com/shunmei/cc-clip/internal/token"
)

func TestStopLocalProcessDoesNotKillUnexpectedCommand(t *testing.T) {
	cmd := helperSleepProcess(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}

	// Use sync.Once to ensure cmd.Wait() is called exactly once,
	// preventing a data race between the cleanup and the goroutine.
	var waitOnce sync.Once
	var waitErr error
	doWait := func() { waitOnce.Do(func() { waitErr = cmd.Wait() }) }

	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		doWait()
	})

	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatalf("failed to write pid file: %v", err)
	}

	stopLocalProcess(pidFile, "Xvfb")
	time.Sleep(100 * time.Millisecond)

	waitDone := make(chan struct{}, 1)
	go func() {
		doWait()
		waitDone <- struct{}{}
	}()

	select {
	case <-waitDone:
		t.Fatalf("unexpected command should still be running, but exited early: %v", waitErr)
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

func TestGetPortFlagOverridesEnv(t *testing.T) {
	oldArgs := os.Args
	oldEnv, hadEnv := os.LookupEnv("CC_CLIP_PORT")
	t.Cleanup(func() {
		os.Args = oldArgs
		if hadEnv {
			_ = os.Setenv("CC_CLIP_PORT", oldEnv)
		} else {
			_ = os.Unsetenv("CC_CLIP_PORT")
		}
	})

	os.Args = []string{"cc-clip", "serve", "--port", "19000"}
	if err := os.Setenv("CC_CLIP_PORT", "18000"); err != nil {
		t.Fatalf("set env: %v", err)
	}

	if got := getPort(); got != 19000 {
		t.Fatalf("getPort() = %d, want CLI flag to override env with 19000", got)
	}
}

func TestManualFlagParsingSupportsEqualsForm(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	os.Args = []string{"cc-clip", "connect", "venus", "--port=19001", "--local-bin=/tmp/cc-clip", "--force=true"}

	if got := getPort(); got != 19001 {
		t.Fatalf("getPort() = %d, want 19001", got)
	}
	if got := getFlag("local-bin", "fallback"); got != "/tmp/cc-clip" {
		t.Fatalf("getFlag(local-bin) = %q", got)
	}
	if !hasFlag("force") {
		t.Fatal("hasFlag(force) should accept --force=true")
	}
}

func TestManualFlagParsingRejectsExplicitFalseBool(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	os.Args = []string{"cc-clip", "connect", "venus", "--force=false"}

	if hasFlag("force") {
		t.Fatal("hasFlag(force) should not treat --force=false as enabled")
	}
}

type testRemoteSession struct {
	home string
}

func (s *testRemoteSession) Exec(cmd string) (string, error) {
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "HOME="+s.home, "PATH=/usr/bin:/bin")
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

func (s *testRemoteSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "HOME="+s.home, "PATH=/usr/bin:/bin")
	c.Stdin = stdin
	out, err := c.CombinedOutput()
	return string(out), err
}

func TestConfigureRemoteClaudeHooksInstallsSettingsWithoutWrapper(t *testing.T) {
	s := &testRemoteSession{home: t.TempDir()}

	configureRemoteClaudeHooks(s, 18339, connectOpts{})

	settings := readTestClaudeSettings(t, s.home)
	if strings.Count(settings, "env CC_CLIP_MANAGED=1 cc-clip-hook") != 2 {
		t.Fatalf("settings should contain two managed hooks, got:\n%s", settings)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".local", "bin", "claude")); !os.IsNotExist(err) {
		t.Fatalf("settings-first install should not create wrapper, got err=%v", err)
	}
}

func TestConfigureRemoteClaudeHooksRemovesLegacyWrapperAfterSettingsInstall(t *testing.T) {
	s := &testRemoteSession{home: t.TempDir()}
	writeTestClaudeWrapper(t, s.home)

	configureRemoteClaudeHooks(s, 18339, connectOpts{})

	settings := readTestClaudeSettings(t, s.home)
	if !strings.Contains(settings, "env CC_CLIP_MANAGED=1 cc-clip-hook") {
		t.Fatalf("settings missing managed hook:\n%s", settings)
	}
	claude, err := os.ReadFile(filepath.Join(s.home, ".local", "bin", "claude"))
	if err != nil {
		t.Fatalf("claude should be restored from sidecar: %v", err)
	}
	if !strings.Contains(string(claude), "echo restored") {
		t.Fatalf("legacy wrapper was not restored from sidecar:\n%s", claude)
	}
}

func TestConfigureRemoteClaudeHooksHonorsExistingNoHooksMarker(t *testing.T) {
	s := &testRemoteSession{home: t.TempDir()}
	marker := filepath.Join(s.home, ".cache", "cc-clip", "no-hooks")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	configureRemoteClaudeHooks(s, 18339, connectOpts{})

	if _, err := os.Stat(filepath.Join(s.home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("opt-out marker should prevent settings install, got err=%v", err)
	}
}

func TestConfigureRemoteClaudeHooksNoHooksRemovesManagedSettingsAndWrapper(t *testing.T) {
	s := &testRemoteSession{home: t.TempDir()}
	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "env CC_CLIP_MANAGED=1 cc-clip-hook"},
          {"type": "command", "command": "cc-clip-hook"}
        ]
      }
    ]
  }
}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	writeTestClaudeWrapper(t, s.home)

	configureRemoteClaudeHooks(s, 18339, connectOpts{noHooks: true})

	settings := readTestClaudeSettings(t, s.home)
	if strings.Contains(settings, "env CC_CLIP_MANAGED=1 cc-clip-hook") {
		t.Fatalf("managed hook should be removed:\n%s", settings)
	}
	if !strings.Contains(settings, `"cc-clip-hook"`) {
		t.Fatalf("user-managed hook should remain:\n%s", settings)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".cache", "cc-clip", "no-hooks")); err != nil {
		t.Fatalf("no-hooks marker should exist: %v", err)
	}
	claude, err := os.ReadFile(filepath.Join(s.home, ".local", "bin", "claude"))
	if err != nil {
		t.Fatalf("claude should be restored from sidecar: %v", err)
	}
	if !strings.Contains(string(claude), "echo restored") {
		t.Fatalf("legacy wrapper was not restored from sidecar:\n%s", claude)
	}
}

func readTestClaudeSettings(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read Claude settings: %v", err)
	}
	return string(data)
}

func writeTestClaudeWrapper(t *testing.T, home string) {
	t.Helper()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("# cc-clip claude wrapper\n"), 0755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude.cc-clip-real"), []byte("#!/bin/sh\necho restored\n"), 0755); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func TestNewDeployStateReturnsHashError(t *testing.T) {
	_, err := newDeployState("/nonexistent/cc-clip", "v0.7.2", "xclip", true, nil, false)
	if err == nil {
		t.Fatal("newDeployState should return an error when local binary hashing fails")
	}
}

func TestNewDeployStatePreservesCodexWhenNotRequested(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cc-clip")
	if err := os.WriteFile(binPath, []byte("binary"), 0755); err != nil {
		t.Fatalf("failed to write test binary: %v", err)
	}
	existingCodex := &shim.CodexDeployState{
		Enabled:      true,
		Mode:         "x11-bridge",
		DisplayFixed: true,
	}
	existingNotify := &shim.NotifyDeployState{
		Enabled:        true,
		HookInstalled:  true,
		CodexInjected:  true,
		HealthVerified: true,
	}
	existingWrapper := &shim.ClaudeWrapperState{
		Installed:  true,
		OriginKind: "symlink",
	}

	state, err := newDeployState(binPath, "v0.7.2", "xclip", true, &shim.DeployState{
		Codex:         existingCodex,
		Notify:        existingNotify,
		ClaudeWrapper: existingWrapper,
	}, false)
	if err != nil {
		t.Fatalf("newDeployState returned error: %v", err)
	}
	if state.BinaryHash == "" {
		t.Fatal("newDeployState should populate BinaryHash")
	}
	if state.Codex != existingCodex {
		t.Fatal("newDeployState should preserve existing Codex state when --codex is not requested")
	}
	if state.Notify != existingNotify {
		t.Fatal("newDeployState should preserve existing Notify state")
	}
	if state.ClaudeWrapper != existingWrapper {
		t.Fatal("newDeployState should preserve existing ClaudeWrapper state")
	}
}

func TestNewDeployStateDoesNotPreserveCodexWhenRequested(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cc-clip")
	if err := os.WriteFile(binPath, []byte("binary"), 0755); err != nil {
		t.Fatalf("failed to write test binary: %v", err)
	}

	state, err := newDeployState(binPath, "v0.7.2", "xclip", true, &shim.DeployState{
		Codex: &shim.CodexDeployState{
			Enabled:      true,
			Mode:         "x11-bridge",
			DisplayFixed: true,
		},
	}, true)
	if err != nil {
		t.Fatalf("newDeployState returned error: %v", err)
	}
	if state.Codex != nil {
		t.Fatal("newDeployState should let --codex rebuild Codex state")
	}
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
	if !msg.Verified {
		t.Fatal("Codex notify payload should be treated as trusted local config")
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

	if err := registerNonceWithDaemon(port, sess.Token, testNonce, ""); err != nil {
		t.Fatalf("registerNonceWithDaemon failed: %v", err)
	}

	// Verify the nonce works by sending a health probe
	if err := runNotificationHealthProbe(port, testNonce); err != nil {
		t.Fatalf("health probe failed after nonce registration: %v", err)
	}
}

// TestRegisterNonceWithDaemonHostRevokesPrior asserts the client→daemon
// wiring: a second registration for the same host invalidates the first
// nonce immediately, instead of waiting on TTL or FIFO eviction. This
// regression-guards the host parameter threading through main.go +
// connectNotifySetup + registerNonceWithDaemon + handleRegisterNonce.
func TestRegisterNonceWithDaemonHostRevokesPrior(t *testing.T) {
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

	oldNonce := "old-nonce-0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"
	newNonce := "new-nonce-fedcba9876543210fedcba9876543210fedcba9876543210fedcba98765432"
	const host = "venus.example"

	if err := registerNonceWithDaemon(port, sess.Token, oldNonce, host); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := runNotificationHealthProbe(port, oldNonce); err != nil {
		t.Fatalf("old nonce should be valid right after registration: %v", err)
	}

	if err := registerNonceWithDaemon(port, sess.Token, newNonce, host); err != nil {
		t.Fatalf("second register: %v", err)
	}
	if err := runNotificationHealthProbe(port, newNonce); err != nil {
		t.Fatalf("new nonce should be valid after reregistration: %v", err)
	}
	if err := runNotificationHealthProbe(port, oldNonce); err == nil {
		t.Fatal("same-host reconnect must revoke the previous nonce, but old nonce still authenticates")
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
		Title:    "Build complete",
		Body:     "All tests passed",
		Urgency:  1,
		Sound:    "Ping",
		Verified: true,
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
		if env.GenericMessage.Sound != msg.Sound {
			t.Fatalf("expected sound %q, got %q", msg.Sound, env.GenericMessage.Sound)
		}
		if !env.GenericMessage.Verified {
			t.Fatal("expected trusted notification to be verified")
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

func TestClaudeHookConfigJSONUsesClaudeV2HookSchema(t *testing.T) {
	cfg := claudeHookConfigJSON()
	assertClaudeV2HookSchema(t, cfg)
}

type claudeHookSettings struct {
	Hooks map[string][]claudeHookMatcher `json:"hooks"`
}

type claudeHookMatcher struct {
	Matcher string              `json:"matcher"`
	Hooks   []claudeHookCommand `json:"hooks"`
}

type claudeHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func assertClaudeV2HookSchema(t *testing.T, cfg string) {
	t.Helper()

	var settings claudeHookSettings
	if err := json.Unmarshal([]byte(cfg), &settings); err != nil {
		t.Fatalf("hook config is not valid JSON: %v\n%s", err, cfg)
	}
	for _, event := range []string{"Notification", "Stop"} {
		matchers := settings.Hooks[event]
		if len(matchers) != 1 {
			t.Fatalf("%s: expected exactly one matcher entry, got %#v", event, matchers)
		}
		if matchers[0].Matcher != "" {
			t.Fatalf("%s: expected empty matcher, got %q", event, matchers[0].Matcher)
		}
		if len(matchers[0].Hooks) != 1 {
			t.Fatalf("%s: expected exactly one hook command, got %#v", event, matchers[0].Hooks)
		}
		cmd := matchers[0].Hooks[0]
		if cmd.Type != "command" || cmd.Command != "cc-clip-hook" {
			t.Fatalf("%s: unexpected hook command: %#v", event, cmd)
		}
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
