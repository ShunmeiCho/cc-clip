package daemon

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ClassifyHookPayload translates a Claude Code hook JSON payload into a
// structured NotifyEnvelope. hookType is the hook category ("notification",
// "stop", etc.) and raw is the decoded JSON body.
func ClassifyHookPayload(hookType string, raw map[string]any) *NotifyEnvelope {
	host, _ := raw["_cc_clip_host"].(string)
	env := &NotifyEnvelope{
		Kind:      KindToolAttention,
		Source:    "claude_hook",
		Host:      host,
		Timestamp: time.Now().UTC(),
		ToolAttention: &ToolAttentionPayload{
			HookType: hookType,
			Verified: true,
		},
		GenericMessage: &GenericMessagePayload{Verified: true},
	}

	switch hookType {
	case "notification":
		notifType, _ := raw["type"].(string)
		title, _ := raw["title"].(string)
		body, _ := raw["body"].(string)
		env.ToolAttention.NotifType = notifType
		env.GenericMessage.Body = body
		switch notifType {
		case "permission_prompt":
			env.GenericMessage.Title = "Tool approval needed"
			env.GenericMessage.Urgency = 2
		case "idle_prompt":
			env.GenericMessage.Title = "Claude is idle"
			env.GenericMessage.Urgency = 1
		default:
			env.GenericMessage.Title = title
			env.GenericMessage.Urgency = 1
		}
	case "stop":
		reason, _ := raw["stop_hook_reason"].(string)
		msg, _ := raw["last_assistant_message"].(string)
		env.ToolAttention.StopReason = reason
		env.ToolAttention.Message = truncate(msg, 280)
		env.GenericMessage.Body = truncate(msg, 280)
		if reason == "stop_at_end_of_turn" {
			env.GenericMessage.Title = "Claude finished"
			env.GenericMessage.Urgency = 0
		} else {
			env.GenericMessage.Title = "Claude stopped"
			env.GenericMessage.Urgency = 1
		}
	default:
		env.Kind = KindGenericMessage
		env.Source = "claude_hook"
		env.GenericMessage.Title = fmt.Sprintf("Claude hook: %s", hookType)
		env.GenericMessage.Body = truncate(stringifyMap(raw), 280)
		env.GenericMessage.Urgency = 1
		env.ToolAttention = nil
	}

	return env
}

// truncate trims whitespace and limits s to at most limit bytes,
// appending an ellipsis if truncation occurs.
func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "\u2026"
}

// stringifyMap produces a deterministic "key=value, key=value" string from
// a map, sorted by key. Used for the default classifier case.
func stringifyMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(m))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
