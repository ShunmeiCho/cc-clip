package daemon

import (
	"testing"
)

func TestClassifyHookPayload(t *testing.T) {
	tests := []struct {
		name      string
		hookType  string
		raw       map[string]any
		wantTitle string
		wantUrg   int
		wantType  string
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
			wantType:  "permission_prompt",
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
			wantType:  "stop_at_end_of_turn",
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
			wantType:  "idle_prompt",
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
			wantType:  "interrupted",
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
			wantType:  "custom_event",
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
			wantType:  "progress_update",
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
		"type":            "permission_prompt",
		"body":            "approve",
		"_cc_clip_host":   "devbox-01",
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
	// truncate(s, 280) yields at most 279 ASCII bytes + multi-byte ellipsis.
	// The result must be strictly shorter than the 300-byte input.
	if len(env.GenericMessage.Body) >= 300 {
		t.Fatalf("expected body truncated below 300 bytes, got len=%d", len(env.GenericMessage.Body))
	}
	if len(env.GenericMessage.Body) == 0 {
		t.Fatal("expected non-empty body after truncation")
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
		{"over limit", "hello world", 6, "hello\u2026"},
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
