package plugin

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

// mockClipboard satisfies daemon.ClipboardReader without touching the OS.
type mockClipboard struct{}

func (mockClipboard) Type() (daemon.ClipboardInfo, error) {
	return daemon.ClipboardInfo{Type: daemon.ClipboardEmpty}, nil
}
func (mockClipboard) ImageBytes() ([]byte, error) { return nil, nil }

// drainOne reads exactly one envelope off the server's notify channel, failing
// if none arrives promptly. It is reused across delivery assertions.
func drainOne(t *testing.T, ch <-chan daemon.NotifyEnvelope) daemon.NotifyEnvelope {
	t.Helper()
	select {
	case env := <-ch:
		return env
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for envelope")
		return daemon.NotifyEnvelope{}
	}
}

// TestCodexNotifyParseMatchesMainParser guards the duplicated parser copy: the
// plugin package's parseCodexNotifyPayload must produce the same fields that
// main.parseCodexNotifyPayload produces today.
func TestCodexNotifyParseMatchesMainParser(t *testing.T) {
	msg, err := parseCodexNotifyPayload(`{"last-assistant-message":"hello world"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Title != "Codex" {
		t.Fatalf("title = %q, want %q", msg.Title, "Codex")
	}
	if msg.Body != "hello world" {
		t.Fatalf("body = %q, want %q", msg.Body, "hello world")
	}
	if msg.Urgency != 1 {
		t.Fatalf("urgency = %d, want 1", msg.Urgency)
	}
	if !msg.Verified {
		t.Fatal("Verified should be true")
	}
}

func TestCodexNotifyParseRejectsInvalidJSON(t *testing.T) {
	if _, err := parseCodexNotifyPayload(`{invalid`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestPostNotificationDelivers asserts PostNotification reproduces the wire
// behavior of main.postGenericNotification: it reads the nonce file and POSTs a
// generic JSON notification the daemon accepts.
func TestPostNotificationDelivers(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	msg := daemon.GenericMessagePayload{
		Title:    "Build complete",
		Body:     "All tests passed",
		Urgency:  1,
		Sound:    "Ping",
		Verified: true,
	}
	if err := PostNotification(port, msg); err != nil {
		t.Fatalf("PostNotification failed: %v", err)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.GenericMessage == nil {
		t.Fatal("expected GenericMessage payload")
	}
	if env.GenericMessage.Title != msg.Title {
		t.Fatalf("title = %q, want %q", env.GenericMessage.Title, msg.Title)
	}
	if env.GenericMessage.Body != msg.Body {
		t.Fatalf("body = %q, want %q", env.GenericMessage.Body, msg.Body)
	}
	if env.GenericMessage.Sound != msg.Sound {
		t.Fatalf("sound = %q, want %q", env.GenericMessage.Sound, msg.Sound)
	}
	if !env.GenericMessage.Verified {
		t.Fatal("Verified should propagate from trusted=true")
	}
}

// newNotifyServerWithChannel is like newNotifyServer but returns the live server
// so tests can drain the notify channel.
func newNotifyServerWithChannel(t *testing.T) (port int, srv *daemon.Server) {
	t.Helper()
	tm := token.NewManager(time.Hour)
	if _, err := tm.Generate(); err != nil {
		t.Fatalf("generate token: %v", err)
	}
	store := session.NewStore(12 * time.Hour)
	srv = daemon.NewServer("127.0.0.1:0", mockClipboard{}, tm, store)
	nonce := "test-notify-nonce-0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := srv.RegisterNotificationNonce(nonce); err != nil {
		t.Fatalf("register nonce: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, _ = strconv.Atoi(portStr)

	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache", "cc-clip")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "notify.nonce"), []byte(nonce+"\n"), 0600); err != nil {
		t.Fatalf("write nonce: %v", err)
	}
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	return port, srv
}

// TestPostHookPayloadForwardsRawHookJSON asserts postHookPayload forwards the
// raw hook JSON with the claude-hook content type so the daemon classifies it,
// reproducing the cc-clip-hook bash flow's payload semantics.
func TestPostHookPayloadForwardsRawHookJSON(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	raw := []byte(`{"hook_event_name":"Stop","stop_hook_reason":"stop_at_end_of_turn","last_assistant_message":"done"}`)
	if err := postHookPayload(port, raw); err != nil {
		t.Fatalf("postHookPayload failed: %v", err)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.Source != "claude_hook" {
		t.Fatalf("source = %q, want claude_hook (proves hook content-type path)", env.Source)
	}
	if env.GenericMessage == nil || env.GenericMessage.Title != "Claude finished" {
		t.Fatalf("expected classifier to map stop_at_end_of_turn to 'Claude finished', got %+v", env.GenericMessage)
	}
}

// TestRunClaudeNotifyForwardsStdin asserts Run("claude-notify") reads raw hook
// JSON from stdin and forwards it via the hook content-type path.
func TestRunClaudeNotifyForwardsStdin(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	stdin := strings.NewReader(`{"hook_event_name":"Stop","stop_hook_reason":"stop_at_end_of_turn","last_assistant_message":"hi"}`)
	var stdout strings.Builder
	if err := Run(AdapterClaudeNotify, port, stdin, &stdout); err != nil {
		t.Fatalf("Run claude-notify failed: %v", err)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.Source != "claude_hook" {
		t.Fatalf("source = %q, want claude_hook", env.Source)
	}
}

// TestRunCodexNotifyParsesAndPosts asserts Run("codex-notify") parses the codex
// payload from stdin and posts it as a generic notification.
func TestRunCodexNotifyParsesAndPosts(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	stdin := strings.NewReader(`{"last-assistant-message":"codex says hi"}`)
	var stdout strings.Builder
	if err := Run(AdapterCodexNotify, port, stdin, &stdout); err != nil {
		t.Fatalf("Run codex-notify failed: %v", err)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.GenericMessage == nil {
		t.Fatal("expected GenericMessage payload")
	}
	if env.GenericMessage.Title != "Codex" {
		t.Fatalf("title = %q, want Codex", env.GenericMessage.Title)
	}
	if env.GenericMessage.Body != "codex says hi" {
		t.Fatalf("body = %q, want %q", env.GenericMessage.Body, "codex says hi")
	}
}

// TestRunAntigravityNotifyAlwaysWritesDecision asserts the antigravity adapter
// writes {"decision":""} to stdout on success and posts the notification.
func TestRunAntigravityNotifyAlwaysWritesDecision(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	stdin := strings.NewReader(`{"last-assistant-message":"agy says hi"}`)
	var stdout strings.Builder
	if err := Run(AdapterAntigravityNotify, port, stdin, &stdout); err != nil {
		t.Fatalf("Run antigravity-notify returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"decision":""}` {
		t.Fatalf("stdout = %q, want %q", got, `{"decision":""}`)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.GenericMessage == nil || env.GenericMessage.Title != "Codex" {
		t.Fatalf("expected codex-style parse, got %+v", env.GenericMessage)
	}
}

// TestRunAntigravityNotifyWritesDecisionOnPostFailure asserts the decision JSON
// is still written when the POST fails (no nonce file / unreachable daemon), and
// Run returns nil so 'agy' is never blocked.
func TestRunAntigravityNotifyWritesDecisionOnPostFailure(t *testing.T) {
	// Point HOME at an empty dir so the nonce file is missing => POST fails.
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })

	// Use a port unlikely to be serving; the missing nonce alone forces failure.
	stdin := strings.NewReader(`{"last-assistant-message":"agy"}`)
	var stdout strings.Builder
	if err := Run(AdapterAntigravityNotify, 1, stdin, &stdout); err != nil {
		t.Fatalf("antigravity Run must return nil even on POST failure, got %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"decision":""}` {
		t.Fatalf("stdout = %q, want %q even on failure", got, `{"decision":""}`)
	}
}

func TestRunUnknownAdapter(t *testing.T) {
	var stdout strings.Builder
	err := Run("does-not-exist", 18339, strings.NewReader(""), &stdout)
	if err == nil {
		t.Fatal("expected error for unknown adapter")
	}
	if !strings.Contains(err.Error(), "unknown plugin adapter") {
		t.Fatalf("error = %q, want it to mention unknown plugin adapter", err.Error())
	}
}
