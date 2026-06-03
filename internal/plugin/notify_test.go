package plugin

import (
	"strings"
	"testing"
)

// TestParseOpencodeNotifyPayload is a table test for the opencode event-envelope
// parser. The JS plugin sends JSON.stringify({ event }), so the opencode event
// object is nested under "event"; session.idle maps to a fixed body, any other
// type echoes the type string, and malformed JSON returns an error.
func TestParseOpencodeNotifyPayload(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantTitle string
		wantBody  string
		wantUrg   int
		wantVer   bool
	}{
		{name: "session idle", input: `{"event":{"type":"session.idle"}}`, wantTitle: "opencode", wantBody: "Session idle - awaiting input", wantUrg: 1, wantVer: true},
		{name: "session idle with properties", input: `{"event":{"type":"session.idle","properties":{"sessionID":"abc"}}}`, wantTitle: "opencode", wantBody: "Session idle - awaiting input", wantUrg: 1, wantVer: true},
		{name: "unknown type echoes", input: `{"event":{"type":"some.other.event"}}`, wantTitle: "opencode", wantBody: "some.other.event", wantUrg: 1, wantVer: true},
		{name: "missing event", input: `{"foo":"bar"}`, wantTitle: "opencode", wantBody: "", wantUrg: 1, wantVer: true},
		{name: "invalid json", input: `{invalid`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := parseOpencodeNotifyPayload(tt.input)
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

// TestOpencodeBody asserts the body mapping: session.idle is the only special
// case; any other event type echoes its type string.
func TestOpencodeBody(t *testing.T) {
	if got := opencodeBody("session.idle"); got != "Session idle - awaiting input" {
		t.Fatalf("opencodeBody(session.idle) = %q, want %q", got, "Session idle - awaiting input")
	}
	if got := opencodeBody("session.error"); got != "session.error" {
		t.Fatalf("opencodeBody(session.error) = %q, want echoed type", got)
	}
}

// TestRunOpencodeNotifyFailSoftOnParseError asserts an invalid payload does NOT
// propagate from runOpencodeNotify (fire-and-forget; opencode must not be
// disrupted by a malformed event).
func TestRunOpencodeNotifyFailSoftOnParseError(t *testing.T) {
	stdin := strings.NewReader(`{invalid`)
	if err := runOpencodeNotify(18339, stdin); err != nil {
		t.Fatalf("runOpencodeNotify must be fail-soft on parse error, got %v", err)
	}
}

// TestRunOpencodeNotifyFailSoftOnReadError asserts a stdin read error does NOT
// propagate from runOpencodeNotify.
func TestRunOpencodeNotifyFailSoftOnReadError(t *testing.T) {
	if err := runOpencodeNotify(18339, errReader{}); err != nil {
		t.Fatalf("runOpencodeNotify must be fail-soft on read error, got %v", err)
	}
}

// TestRunOpencodeNotifyFailSoftOnEmptyStdin asserts empty stdin does NOT panic
// or propagate (empty body parses to an empty event => still fail-soft).
func TestRunOpencodeNotifyFailSoftOnEmptyStdin(t *testing.T) {
	if err := runOpencodeNotify(18339, strings.NewReader("")); err != nil {
		t.Fatalf("runOpencodeNotify must be fail-soft on empty stdin, got %v", err)
	}
}

// TestRunOpencodeNotifyFailSoftOnPostFailure asserts a POST failure (missing
// nonce) does NOT propagate from runOpencodeNotify.
func TestRunOpencodeNotifyFailSoftOnPostFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // empty => nonce file missing => POST fails

	stdin := strings.NewReader(`{"event":{"type":"session.idle"}}`)
	if err := runOpencodeNotify(1, stdin); err != nil {
		t.Fatalf("runOpencodeNotify must not propagate POST failure, got %v", err)
	}
}
