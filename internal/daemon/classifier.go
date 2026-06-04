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
		if body == "" {
			body, _ = raw["message"].(string)
		}
		env.ToolAttention.NotifType = notifType
		env.ToolAttention.Message = truncate(body, 280)
		env.GenericMessage.Body = body
		switch notifType {
		case "permission_prompt":
			env.GenericMessage.Title = "Tool approval needed"
			env.GenericMessage.Urgency = 2
			// Sound is intentionally left unset here. The critical-tier
			// default (and any CC_CLIP_SOUND_CRITICAL override) is applied at
			// delivery time by defaultSoundForUrgency, keeping all
			// sound-selection logic in one configurable place.
		case "idle_prompt":
			env.GenericMessage.Title = "Claude is idle"
			env.GenericMessage.Urgency = 1
		default:
			env.GenericMessage.Title = title
			if env.GenericMessage.Title == "" {
				// Neither a recognized subtype nor a title was supplied;
				// fall back to a diagnostic title so the notification is
				// not delivered near-blank.
				if notifType != "" {
					env.GenericMessage.Title = fmt.Sprintf("Claude notification: %s", notifType)
				} else {
					env.GenericMessage.Title = "Claude notification"
				}
			}
			env.GenericMessage.Urgency = 1
		}
	case "stop":
		reason, _ := raw["stop_hook_reason"].(string)
		msg, _ := raw["last_assistant_message"].(string)
		env.ToolAttention.StopReason = reason
		env.ToolAttention.Message = truncate(msg, 280)
		env.GenericMessage.Body = truncate(msg, 280)
		// Stop fires when the agent finished responding (a calm end-of-turn).
		// The real Claude Code Stop payload has no stop_hook_reason field, so
		// an absent/empty reason is the normal-completion case. Only a
		// non-empty reason that is not the explicit completion signal (old or
		// abnormal payloads) is treated as an interrupted stop.
		if reason == "" || reason == "stop_at_end_of_turn" {
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

// ellipsis is the single-character truncation marker. As UTF-8 it is 3 bytes.
const ellipsis = "\u2026"

// truncate trims whitespace and, if the result exceeds limit bytes, cuts it
// at a UTF-8 rune boundary and appends an ellipsis so the final string never
// exceeds limit bytes and never splits a multi-byte rune. The byte budget for
// content is limit minus len(ellipsis); ranging over a string yields rune
// start offsets, so the last offset that still fits the budget is the cut
// point.
func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	budget := limit - len(ellipsis)
	if budget <= 0 {
		return ellipsis
	}
	cut := 0
	for i := range s {
		if i > budget {
			break
		}
		cut = i
	}
	return s[:cut] + ellipsis
}

// internalKeyPrefix marks map keys that cc-clip injects for internal use
// (e.g. "_cc_clip_host"). These must never surface in user-facing display text.
const internalKeyPrefix = "_cc_clip_"

// stringifyMap produces a deterministic "key=value, key=value" string from
// a map, sorted by key. Used for the default classifier case. Keys prefixed
// with internalKeyPrefix are skipped so cc-clip's internal metadata does not
// leak into the displayed notification body.
func stringifyMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if strings.HasPrefix(k, internalKeyPrefix) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
