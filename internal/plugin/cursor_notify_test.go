package plugin

import (
	"bytes"
	"strings"
	"testing"
)

// TestParseCursorNotifyPayload is the table test for the Cursor stop-hook
// parser. Only `status` carries meaning; the rest of the envelope identifies
// the session and must not reach the notification body.
func TestParseCursorNotifyPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantTitle string
		wantBody  string
		wantUrg   int
	}{
		{
			name:      "completed",
			input:     `{"status":"completed","conversation_id":"abc","model":"gpt-5"}`,
			wantTitle: "Cursor",
			wantBody:  "Turn finished - awaiting input",
			wantUrg:   1,
		},
		{
			name:      "aborted",
			input:     `{"status":"aborted"}`,
			wantTitle: "Cursor",
			wantBody:  "Turn aborted",
			wantUrg:   1,
		},
		{
			name:      "error raises urgency",
			input:     `{"status":"error"}`,
			wantTitle: "Cursor",
			wantBody:  "Turn ended with an error",
			wantUrg:   2,
		},
		{
			name:      "unknown status falls back, never echoes",
			input:     `{"status":"something-new"}`,
			wantTitle: "Cursor",
			wantBody:  "Turn finished - awaiting input",
			wantUrg:   1,
		},
		{
			name:      "missing status",
			input:     `{"conversation_id":"abc"}`,
			wantTitle: "Cursor",
			wantBody:  "Turn finished - awaiting input",
			wantUrg:   1,
		},
		{name: "invalid json", input: `{invalid`, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg, err := parseCursorNotifyPayload(tt.input)
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
				t.Errorf("title = %q, want %q", msg.Title, tt.wantTitle)
			}
			if msg.Body != tt.wantBody {
				t.Errorf("body = %q, want %q", msg.Body, tt.wantBody)
			}
			if msg.Urgency != tt.wantUrg {
				t.Errorf("urgency = %d, want %d", msg.Urgency, tt.wantUrg)
			}
			if !msg.Verified {
				t.Error("verified = false; every string in this message is authored here")
			}
		})
	}
}

// TestParseCursorNotifyPayloadNeverEchoesRemoteText guards the Verified claim.
// The adapter marks its messages verified, which is only honest while none of
// the payload's own strings reach the body — otherwise the daemon's
// "[unverified]" marker would be withheld from text cc-clip did not write.
func TestParseCursorNotifyPayloadNeverEchoesRemoteText(t *testing.T) {
	t.Parallel()

	const marker = "SENTINEL-FROM-PAYLOAD"
	input := `{"status":"` + marker + `","model":"` + marker + `","transcript_path":"/tmp/` + marker + `"}`

	msg, err := parseCursorNotifyPayload(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Contains(msg.Body, marker) || strings.Contains(msg.Title, marker) {
		t.Fatalf("payload text leaked into a message marked Verified: %q / %q", msg.Title, msg.Body)
	}
}

// TestRunCursorNotifyWritesNothingToStdout is the one that protects the user
// from an infinite agent loop: Cursor reads a stop hook's stdout as an optional
// {"followup_message": ...} and restarts the agent when it finds one. A notify
// adapter must stay silent.
func TestRunCursorNotifyWritesNothingToStdout(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home) // no nonce file => POST fails, which must not matter here

	var stdout bytes.Buffer
	if err := Run(AdapterCursorNotify, 1, strings.NewReader(`{"status":"completed"}`), &stdout); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("adapter wrote %q to stdout; Cursor would read that as a followup_message", stdout.String())
	}
}

// TestRunCursorNotifyFailSoft asserts none of the failure modes propagate. The
// stop hook runs on Cursor's critical path at the end of every turn; a notify
// failure must never surface as a hook failure.
func TestRunCursorNotifyFailSoft(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	cases := map[string]func() error{
		"invalid payload": func() error { return runCursorNotify(18339, strings.NewReader(`{invalid`)) },
		"read error":      func() error { return runCursorNotify(18339, errReader{}) },
		"empty stdin":     func() error { return runCursorNotify(18339, strings.NewReader("")) },
		"post failure":    func() error { return runCursorNotify(1, strings.NewReader(`{"status":"completed"}`)) },
	}
	for name, run := range cases {
		if err := run(); err != nil {
			t.Errorf("%s: runCursorNotify must be fail-soft, got %v", name, err)
		}
	}
}
