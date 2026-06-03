package daemon

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClassifyHookPayload(t *testing.T) {
	tests := []struct {
		name      string
		hookType  string
		raw       map[string]any
		wantTitle string
		wantUrg   int
	}{
		{
			name:     "permission prompt is critical",
			hookType: "notification",
			raw: map[string]any{
				"type":  "permission_prompt",
				"title": "Approve tool",
				"body":  "Claude wants to Edit cmd/main.go",
			},
			wantTitle: "Tool approval needed",
			wantUrg:   2,
		},
		{
			name:     "stop at end of turn is low urgency",
			hookType: "stop",
			raw: map[string]any{
				"stop_hook_reason":       "stop_at_end_of_turn",
				"last_assistant_message": "Done implementing bridge",
			},
			wantTitle: "Claude finished",
			wantUrg:   0,
		},
		{
			name:     "idle prompt is medium urgency",
			hookType: "notification",
			raw: map[string]any{
				"type":  "idle_prompt",
				"title": "Waiting for input",
				"body":  "Claude is waiting",
			},
			wantTitle: "Claude is idle",
			wantUrg:   1,
		},
		{
			name:     "stop with non-end-of-turn reason",
			hookType: "stop",
			raw: map[string]any{
				"stop_hook_reason":       "interrupted",
				"last_assistant_message": "Was working on...",
			},
			wantTitle: "Claude stopped",
			wantUrg:   1,
		},
		{
			// The real Claude Code Stop hook payload carries no
			// stop_hook_reason field; Stop fires at a calm end-of-turn, so
			// a reason-less payload must be treated as normal completion.
			name:     "stop with no reason is normal completion",
			hookType: "stop",
			raw: map[string]any{
				"stop_hook_active":       false,
				"last_assistant_message": "Done implementing bridge",
			},
			wantTitle: "Claude finished",
			wantUrg:   0,
		},
		{
			name:     "unknown hook type falls through to default",
			hookType: "custom_event",
			raw: map[string]any{
				"foo": "bar",
				"baz": "qux",
			},
			wantTitle: "Claude hook: custom_event",
			wantUrg:   1,
		},
		{
			name:     "notification with unknown type gets title from raw",
			hookType: "notification",
			raw: map[string]any{
				"type":  "progress_update",
				"title": "Step 3 of 5",
				"body":  "Running tests...",
			},
			wantTitle: "Step 3 of 5",
			wantUrg:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ClassifyHookPayload(tt.hookType, tt.raw)
			if env == nil || env.GenericMessage == nil {
				t.Fatalf("expected generic message envelope, got %#v", env)
			}
			if env.GenericMessage.Title != tt.wantTitle {
				t.Fatalf("expected title %q, got %q", tt.wantTitle, env.GenericMessage.Title)
			}
			if env.GenericMessage.Urgency != tt.wantUrg {
				t.Fatalf("expected urgency %d, got %d", tt.wantUrg, env.GenericMessage.Urgency)
			}
		})
	}
}

func TestClassifyHookPayloadKindAndSource(t *testing.T) {
	t.Run("notification sets KindToolAttention", func(t *testing.T) {
		env := ClassifyHookPayload("notification", map[string]any{
			"type": "permission_prompt",
			"body": "approve edit",
		})
		if env.Kind != KindToolAttention {
			t.Fatalf("expected kind %q, got %q", KindToolAttention, env.Kind)
		}
		if env.Source != "claude_hook" {
			t.Fatalf("expected source claude_hook, got %q", env.Source)
		}
		if env.ToolAttention == nil {
			t.Fatal("expected non-nil ToolAttention for notification hook")
		}
		if !env.ToolAttention.Verified {
			t.Fatal("expected Verified=true")
		}
	})

	t.Run("stop sets KindToolAttention", func(t *testing.T) {
		env := ClassifyHookPayload("stop", map[string]any{
			"stop_hook_reason": "stop_at_end_of_turn",
		})
		if env.Kind != KindToolAttention {
			t.Fatalf("expected kind %q, got %q", KindToolAttention, env.Kind)
		}
		if env.ToolAttention == nil || env.ToolAttention.StopReason != "stop_at_end_of_turn" {
			t.Fatalf("expected stop reason, got %#v", env.ToolAttention)
		}
	})

	t.Run("unknown hookType sets KindGenericMessage and nil ToolAttention", func(t *testing.T) {
		env := ClassifyHookPayload("something_else", map[string]any{
			"key": "value",
		})
		if env.Kind != KindGenericMessage {
			t.Fatalf("expected kind %q, got %q", KindGenericMessage, env.Kind)
		}
		if env.ToolAttention != nil {
			t.Fatal("expected nil ToolAttention for unknown hookType")
		}
	})
}

func TestClassifyHookPayloadHostExtraction(t *testing.T) {
	env := ClassifyHookPayload("notification", map[string]any{
		"type":          "permission_prompt",
		"body":          "approve",
		"_cc_clip_host": "devbox-01",
	})
	if env.Host != "devbox-01" {
		t.Fatalf("expected host devbox-01, got %q", env.Host)
	}
}

func TestClassifyHookPayloadTruncatesLongMessages(t *testing.T) {
	longMsg := ""
	for i := 0; i < 300; i++ {
		longMsg += "x"
	}
	env := ClassifyHookPayload("stop", map[string]any{
		"stop_hook_reason":       "stop_at_end_of_turn",
		"last_assistant_message": longMsg,
	})
	// truncate(s, 280) keeps at most 280 bytes total (content cut at a rune
	// boundary within a 277-byte budget plus a 3-byte ellipsis), so the
	// result must be strictly shorter than the 300-byte input.
	if len(env.GenericMessage.Body) >= 300 {
		t.Fatalf("expected body truncated below 300 bytes, got len=%d", len(env.GenericMessage.Body))
	}
	if len(env.GenericMessage.Body) == 0 {
		t.Fatal("expected non-empty body after truncation")
	}
}

// TestClassifyNotificationFallsBackWhenTypeAndTitleEmpty verifies that a
// notification hook with neither a "type" nor a "title" still produces a
// non-blank diagnostic title, rather than a near-blank notification.
func TestClassifyNotificationFallsBackWhenTypeAndTitleEmpty(t *testing.T) {
	tests := []struct {
		name      string
		raw       map[string]any
		wantTitle string
	}{
		{
			name:      "no type and no title",
			raw:       map[string]any{"body": "something happened"},
			wantTitle: "Claude notification",
		},
		{
			name:      "empty subtype with no title",
			raw:       map[string]any{"type": "progress_update", "body": "step 3"},
			wantTitle: "Claude notification: progress_update",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ClassifyHookPayload("notification", tt.raw)
			if env == nil || env.GenericMessage == nil {
				t.Fatalf("expected generic message envelope, got %#v", env)
			}
			if env.GenericMessage.Title != tt.wantTitle {
				t.Fatalf("expected title %q, got %q", tt.wantTitle, env.GenericMessage.Title)
			}
		})
	}
}

func TestClassifyNotificationUsesMessageFallbackBody(t *testing.T) {
	env := ClassifyHookPayload("notification", map[string]any{
		"hook_event_name": "Notification",
		"type":            "progress_update",
		"message":         "Script Editor text",
	})
	if env == nil || env.GenericMessage == nil {
		t.Fatalf("expected generic message envelope, got %#v", env)
	}
	if env.GenericMessage.Body != "Script Editor text" {
		t.Fatalf("expected message fallback body, got %q", env.GenericMessage.Body)
	}
}

func TestClassifyPermissionPromptGetsDefaultSound(t *testing.T) {
	env := ClassifyHookPayload("notification", map[string]any{
		"type": "permission_prompt",
		"body": "Approve tool",
	})
	if env == nil || env.GenericMessage == nil {
		t.Fatalf("expected generic message envelope, got %#v", env)
	}
	if env.GenericMessage.Sound != defaultCriticalSound {
		t.Fatalf("expected sound %q, got %q", defaultCriticalSound, env.GenericMessage.Sound)
	}
}

// TestStringifyMapSkipsInternalKeys verifies that internal cc-clip keys
// (prefixed "_cc_clip_") are not leaked into the displayed notification body
// via the default classifier branch.
func TestStringifyMapSkipsInternalKeys(t *testing.T) {
	m := map[string]any{
		"_cc_clip_host": "venus",
		"foo":           "bar",
	}
	got := stringifyMap(m)
	if strings.Contains(got, "_cc_clip_host") {
		t.Fatalf("stringifyMap leaked internal key: %q", got)
	}
	if !strings.Contains(got, "foo=bar") {
		t.Fatalf("stringifyMap dropped public key, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"over limit", "hello world", 6, "hel\u2026"},
		{"empty string", "", 10, ""},
		{"whitespace trimmed", "  hello  ", 10, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.limit)
			if got != tt.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.input, tt.limit, got, tt.want)
			}
		})
	}
}

// TestTruncateIsRuneSafe verifies that truncation never splits a multi-byte
// UTF-8 rune (CJK / emoji), which would otherwise produce invalid UTF-8 in
// the notification body. The project's primary locale is Chinese, so this
// is the common case, not an edge case.
func TestTruncateIsRuneSafe(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
	}{
		// \u4f60\u597d\u4e16\u754c = 4 runes x 3 bytes = 12 bytes. Limit chosen so a
		// naive byte slice would cut mid-rune.
		{"cjk cut mid-rune", "\u4f60\u597d\u4e16\u754c\u4f60\u597d\u4e16\u754c", 10},
		// Emoji are 4 bytes each; a naive cut splits the surrogate.
		{"emoji cut mid-rune", "\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", 10},
		{"mixed ascii and cjk", "hello \u4e16\u754c goodbye \u4e16\u754c", 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.limit)
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q, %d) = %q produced invalid UTF-8", tt.input, tt.limit, got)
			}
			if len(got) > tt.limit {
				t.Fatalf("truncate(%q, %d) = %q exceeds byte budget: len=%d", tt.input, tt.limit, got, len(got))
			}
			if !strings.HasSuffix(got, "\u2026") {
				t.Fatalf("truncate(%q, %d) = %q should end with ellipsis when truncated", tt.input, tt.limit, got)
			}
		})
	}
}

func TestStringifyMap(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		want string
	}{
		{
			name: "single key",
			m:    map[string]any{"foo": "bar"},
			want: "foo=bar",
		},
		{
			name: "empty map",
			m:    map[string]any{},
			want: "",
		},
		{
			name: "non-string value",
			m:    map[string]any{"count": 42},
			want: "count=42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringifyMap(tt.m)
			if tt.name == "single key" || tt.name == "empty map" || tt.name == "non-string value" {
				// For single-key maps, exact match is deterministic
				if got != tt.want {
					t.Fatalf("stringifyMap(%v) = %q, want %q", tt.m, got, tt.want)
				}
			}
		})
	}

	// Multi-key: just check all pairs are present (map iteration order is non-deterministic)
	t.Run("multi key contains all pairs", func(t *testing.T) {
		m := map[string]any{"a": "1", "b": "2"}
		got := stringifyMap(m)
		if len(got) == 0 {
			t.Fatal("expected non-empty result")
		}
		for _, pair := range []string{"a=1", "b=2"} {
			found := false
			for _, seg := range splitOnCommaSpace(got) {
				if seg == pair {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %q in %q", pair, got)
			}
		}
	})
}

// splitOnCommaSpace splits on ", " to check stringifyMap output pairs.
func splitOnCommaSpace(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s)-1; i++ {
		if s[i] == ',' && s[i+1] == ' ' {
			parts = append(parts, s[start:i])
			start = i + 2
		}
	}
	parts = append(parts, s[start:])
	return parts
}
