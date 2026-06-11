package plugin

import (
	"encoding/json"
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
func (mockClipboard) Text() (string, error)       { return "", nil }

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

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	drive := filepath.VolumeName(home)
	if drive != "" {
		t.Setenv("HOMEDRIVE", drive)
		t.Setenv("HOMEPATH", strings.TrimPrefix(home[len(drive):], `\`))
	}
}

// codexParseGolden is the cross-check contract for the duplicated
// parseCodexNotifyPayload copies (internal/plugin here + package main). The
// golden values represent main.parseCodexNotifyPayload's output. The IDENTICAL
// table is driven against the main copy in
// cmd/cc-clip/main_test.go:TestParseCodexNotifyPayloadGolden. If either copy
// drifts, its test fails. KEEP THESE TWO TABLES IN SYNC.
var codexParseGolden = []struct {
	name      string
	input     string
	wantErr   bool
	wantTitle string
	wantBody  string
	wantUrg   int
	wantVer   bool
}{
	{name: "valid", input: `{"last-assistant-message":"hello world"}`, wantTitle: "Codex", wantBody: "hello world", wantUrg: 1, wantVer: true},
	{name: "empty message", input: `{"last-assistant-message":""}`, wantTitle: "Codex", wantBody: "", wantUrg: 1, wantVer: true},
	{name: "missing field", input: `{"some-other-field":"value"}`, wantTitle: "Codex", wantBody: "", wantUrg: 1, wantVer: true},
	{name: "invalid json", input: `{invalid`, wantErr: true},
}

// TestCodexNotifyParseMatchesMainParser guards the duplicated parser copy: the
// plugin package's parseCodexNotifyPayload must produce the same fields that
// main.parseCodexNotifyPayload produces today. It drives the SAME golden table as
// cmd/cc-clip/main_test.go:TestParseCodexNotifyPayloadGolden so any drift between
// the two unexported copies fails this test (or the main-side test).
func TestCodexNotifyParseMatchesMainParser(t *testing.T) {
	for _, tt := range codexParseGolden {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := parseCodexNotifyPayload(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q", msg.Title, tt.wantTitle)
			}
			if msg.Body != tt.wantBody {
				t.Fatalf("body = %q, want %q", msg.Body, tt.wantBody)
			}
			if msg.Urgency != tt.wantUrg {
				t.Fatalf("urgency = %d, want %d", msg.Urgency, tt.wantUrg)
			}
			if msg.Verified != tt.wantVer {
				t.Fatalf("verified = %v, want %v", msg.Verified, tt.wantVer)
			}
		})
	}
}

// TestHostAliasPrefersEnv asserts hostAlias honors CC_CLIP_HOST_ALIAS first,
// matching the bash hook's ${CC_CLIP_HOST_ALIAS:-$(hostname -s)} precedence.
func TestHostAliasPrefersEnv(t *testing.T) {
	t.Setenv("CC_CLIP_HOST_ALIAS", "  my-alias  ")
	if got := hostAlias(); got != "my-alias" {
		t.Fatalf("hostAlias() = %q, want trimmed env value %q", got, "my-alias")
	}
}

// TestHostAliasFallsBackToShortHostname asserts that with no env override,
// hostAlias falls back to a short (domain-stripped) hostname, approximating
// `hostname -s`.
func TestHostAliasFallsBackToShortHostname(t *testing.T) {
	t.Setenv("CC_CLIP_HOST_ALIAS", "")
	got := hostAlias()
	if strings.Contains(got, ".") {
		t.Fatalf("hostAlias() = %q, want domain-stripped short host (no dot)", got)
	}
}

// TestInjectHostSetsKeyOnObject asserts injectHost adds _cc_clip_host to a JSON
// object payload, reproducing the bash hook's python3 injection.
func TestInjectHostSetsKeyOnObject(t *testing.T) {
	t.Setenv("CC_CLIP_HOST_ALIAS", "host-x")
	out := injectHost([]byte(`{"hook_event_name":"Stop"}`))
	var d map[string]interface{}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("injected output not valid JSON: %v (out=%s)", err, out)
	}
	if d["_cc_clip_host"] != "host-x" {
		t.Fatalf("_cc_clip_host = %v, want host-x", d["_cc_clip_host"])
	}
	if d["hook_event_name"] != "Stop" {
		t.Fatalf("original field lost: %+v", d)
	}
}

// TestInjectHostFallsBackOnMalformed asserts injectHost returns the ORIGINAL raw
// bytes unchanged when the payload is not a JSON object, matching the bash
// `|| echo "$_payload"` fallback so malformed/non-object payloads still post.
func TestInjectHostFallsBackOnMalformed(t *testing.T) {
	t.Setenv("CC_CLIP_HOST_ALIAS", "host-x")
	cases := [][]byte{
		[]byte(`{invalid`),        // unparseable
		[]byte(`"just a string"`), // valid JSON but not an object
		[]byte(`null`),            // JSON null => nil map
		[]byte(`[1,2,3]`),         // array, not object
	}
	for _, raw := range cases {
		if got := injectHost(raw); string(got) != string(raw) {
			t.Fatalf("injectHost(%s) = %s, want unchanged original", raw, got)
		}
	}
}

// TestRunClaudeNotifyInjectsHost asserts Run("claude-notify") injects the host
// alias into the forwarded hook JSON (3b-1). The daemon reads _cc_clip_host into
// the envelope's Host field.
func TestRunClaudeNotifyInjectsHost(t *testing.T) {
	t.Setenv("CC_CLIP_HOST_ALIAS", "remote-box")
	port, srv := newNotifyServerWithChannel(t)

	stdin := strings.NewReader(`{"hook_event_name":"Stop","stop_hook_reason":"stop_at_end_of_turn","last_assistant_message":"hi"}`)
	var stdout strings.Builder
	if err := Run(AdapterClaudeNotify, port, stdin, &stdout); err != nil {
		t.Fatalf("Run claude-notify failed: %v", err)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.Host != "remote-box" {
		t.Fatalf("env.Host = %q, want remote-box (proves host injection)", env.Host)
	}
}

// TestRunClaudeNotifyFailSoftOnReadError asserts a stdin read error does NOT
// propagate (3b-1 fail-soft: the hook context requires exit 0).
func TestRunClaudeNotifyFailSoftOnReadError(t *testing.T) {
	if err := runClaudeNotify(18339, errReader{}); err != nil {
		t.Fatalf("runClaudeNotify must be fail-soft on read error, got %v", err)
	}
}

// TestRunClaudeNotifyFailSoftOnPostFailure asserts a POST failure (missing nonce)
// does NOT propagate from runClaudeNotify (3b-1 fail-soft).
func TestRunClaudeNotifyFailSoftOnPostFailure(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home) // empty => nonce file missing => POST fails

	stdin := strings.NewReader(`{"hook_event_name":"Stop"}`)
	if err := runClaudeNotify(1, stdin); err != nil {
		t.Fatalf("runClaudeNotify must not propagate POST failure, got %v", err)
	}
}

// TestRunCodexNotifyFailSoftOnParseError asserts an invalid payload does NOT
// propagate from runCodexNotify (3b-2 fail-soft).
func TestRunCodexNotifyFailSoftOnParseError(t *testing.T) {
	stdin := strings.NewReader(`{invalid`)
	if err := runCodexNotify(18339, stdin); err != nil {
		t.Fatalf("runCodexNotify must be fail-soft on parse error, got %v", err)
	}
}

// TestRunCodexNotifyFailSoftOnPostFailure asserts a POST failure (missing nonce)
// does NOT propagate from runCodexNotify (3b-2 fail-soft).
func TestRunCodexNotifyFailSoftOnPostFailure(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home) // empty => nonce file missing => POST fails

	stdin := strings.NewReader(`{"last-assistant-message":"hi"}`)
	if err := runCodexNotify(1, stdin); err != nil {
		t.Fatalf("runCodexNotify must not propagate POST failure, got %v", err)
	}
}

// errReader always fails, simulating a broken stdin.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errReadFail }

var errReadFail = errReadFailType("simulated read failure")

type errReadFailType string

func (e errReadFailType) Error() string { return string(e) }

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
	setTestHome(t, home)
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
// writes {"decision":""} to stdout on success and posts a notification parsed
// from the Antigravity Stop payload (terminationReason/fullyIdle/error), with
// the title "Antigravity" — NOT the codex last-assistant-message shape.
func TestRunAntigravityNotifyAlwaysWritesDecision(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	stdin := strings.NewReader(`{"terminationReason":"completed","fullyIdle":true}`)
	var stdout strings.Builder
	if err := Run(AdapterAntigravityNotify, port, stdin, &stdout); err != nil {
		t.Fatalf("Run antigravity-notify returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"decision":""}` {
		t.Fatalf("stdout = %q, want %q", got, `{"decision":""}`)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.GenericMessage == nil || env.GenericMessage.Title != "Antigravity" {
		t.Fatalf("expected Antigravity-style parse with title %q, got %+v", "Antigravity", env.GenericMessage)
	}
	if env.GenericMessage.Body != "completed" {
		t.Fatalf("body = %q, want %q (from terminationReason)", env.GenericMessage.Body, "completed")
	}
}

// TestRunAntigravityNotifyWritesDecisionOnPostFailure asserts the decision JSON
// is still written when the POST fails (no nonce file / unreachable daemon), and
// Run returns nil so 'agy' is never blocked.
func TestRunAntigravityNotifyWritesDecisionOnPostFailure(t *testing.T) {
	// Point HOME at an empty dir so the nonce file is missing => POST fails.
	home := t.TempDir()
	setTestHome(t, home)

	// Use a port unlikely to be serving; the missing nonce alone forces failure.
	stdin := strings.NewReader(`{"terminationReason":"user_cancelled"}`)
	var stdout strings.Builder
	if err := Run(AdapterAntigravityNotify, 1, stdin, &stdout); err != nil {
		t.Fatalf("antigravity Run must return nil even on POST failure, got %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"decision":""}` {
		t.Fatalf("stdout = %q, want %q even on failure", got, `{"decision":""}`)
	}
}

// TestRunOpencodeNotifyParsesAndPosts asserts Run("opencode-notify") parses the
// opencode event envelope from stdin and posts it as a generic notification with
// title "opencode".
func TestRunOpencodeNotifyParsesAndPosts(t *testing.T) {
	port, srv := newNotifyServerWithChannel(t)

	stdin := strings.NewReader(`{"event":{"type":"session.idle"}}`)
	var stdout strings.Builder
	if err := Run(AdapterOpencodeNotify, port, stdin, &stdout); err != nil {
		t.Fatalf("Run opencode-notify failed: %v", err)
	}

	env := drainOne(t, srv.NotifyChannel())
	if env.GenericMessage == nil {
		t.Fatal("expected GenericMessage payload")
	}
	if env.GenericMessage.Title != "opencode" {
		t.Fatalf("title = %q, want opencode", env.GenericMessage.Title)
	}
	if env.GenericMessage.Body != "Session idle - awaiting input" {
		t.Fatalf("body = %q, want %q", env.GenericMessage.Body, "Session idle - awaiting input")
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
