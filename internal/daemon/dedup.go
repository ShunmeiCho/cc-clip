package daemon

import (
	"crypto/md5"
	"fmt"
	"sync"
	"time"
)

// DedupKey identifies a notification for deduplication purposes.
type DedupKey struct {
	Source   string
	Type     string
	Title    string
	BodyHash [16]byte
}

// DedupEntry tracks when a notification was last seen and how many
// times it has repeated within the dedup window.
type DedupEntry struct {
	SeenAt time.Time
	Count  int
}

// Deduper suppresses repeated notifications within a configurable time
// window. Critical notifications (Urgency == 2) are never suppressed.
type Deduper struct {
	mu     sync.Mutex
	window time.Duration
	items  map[DedupKey]DedupEntry
}

// NewDeduper creates a Deduper with the given suppression window.
func NewDeduper(window time.Duration) *Deduper {
	return &Deduper{window: window, items: make(map[DedupKey]DedupEntry)}
}

// AllowAt checks whether env should be delivered at the given time.
// Returns (true, nil) if the notification is allowed through.
// Returns (false, &merged) if the notification is suppressed, where
// merged carries the updated DedupCount.
func (d *Deduper) AllowAt(env NotifyEnvelope, now time.Time) (bool, *NotifyEnvelope) {
	if isAlwaysCritical(env) {
		return true, nil
	}
	msg := env.GenericMessage
	if msg == nil {
		return true, nil
	}

	key := DedupKey{
		Source:   env.Source,
		Type:     dedupType(env),
		Title:    msg.Title,
		BodyHash: md5.Sum([]byte(msg.Body)),
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.items[key]
	if !ok || now.Sub(entry.SeenAt) > d.window {
		d.items[key] = DedupEntry{SeenAt: now, Count: 1}
		return true, nil
	}

	entry.Count++
	entry.SeenAt = now
	d.items[key] = entry

	merged := env
	clone := *msg
	clone.DedupCount = entry.Count
	merged.GenericMessage = &clone
	return false, &merged
}

// isAlwaysCritical returns true for notifications that must never be
// suppressed by dedup (e.g., permission prompts).
func isAlwaysCritical(env NotifyEnvelope) bool {
	return env.GenericMessage != nil && env.GenericMessage.Urgency == 2
}

// dedupType extracts a notification subtype string used as part of the
// dedup key. For tool attention events it combines hookType and the
// relevant subtype; otherwise it falls back to the envelope Kind.
func dedupType(env NotifyEnvelope) string {
	if env.ToolAttention != nil {
		switch env.ToolAttention.HookType {
		case "notification":
			return fmt.Sprintf("notification:%s", env.ToolAttention.NotifType)
		case "stop":
			return fmt.Sprintf("stop:%s", env.ToolAttention.StopReason)
		}
	}
	return string(env.Kind)
}
