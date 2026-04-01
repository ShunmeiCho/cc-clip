package daemon

import (
	"testing"
	"time"
)

func TestDeduperSuppressesRepeatedMessagesWithinWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	d := NewDeduper(15 * time.Second)

	env := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "claude_hook",
		GenericMessage: &GenericMessagePayload{
			Title:   "Claude finished",
			Body:    "Done",
			Urgency: 0,
		},
	}

	allowed, merged := d.AllowAt(env, now)
	if !allowed || merged != nil {
		t.Fatalf("first event should pass")
	}

	allowed, merged = d.AllowAt(env, now.Add(5*time.Second))
	if allowed || merged == nil || merged.GenericMessage.DedupCount != 2 {
		t.Fatalf("second event should merge, got allowed=%v merged=%#v", allowed, merged)
	}
}

func TestDeduperNeverSuppressesCriticalPermissionPrompt(t *testing.T) {
	now := time.Unix(1000, 0)
	d := NewDeduper(15 * time.Second)
	env := NotifyEnvelope{
		Kind:   KindToolAttention,
		Source: "claude_hook",
		ToolAttention: &ToolAttentionPayload{
			HookType:  "notification",
			NotifType: "permission_prompt",
			Verified:  true,
		},
		GenericMessage: &GenericMessagePayload{
			Title:   "Tool approval needed",
			Body:    "Claude wants to Edit file",
			Urgency: 2,
		},
	}

	if allowed, _ := d.AllowAt(env, now); !allowed {
		t.Fatal("critical prompt should pass")
	}
	if allowed, _ := d.AllowAt(env, now.Add(2*time.Second)); !allowed {
		t.Fatal("critical prompt should not be deduped")
	}
}

func TestDeduperAllowsAfterWindowExpires(t *testing.T) {
	d := NewDeduper(10 * time.Second)
	now := time.Unix(1000, 0)

	env := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "claude_hook",
		GenericMessage: &GenericMessagePayload{
			Title:   "Claude finished",
			Body:    "Done",
			Urgency: 0,
		},
	}

	allowed, _ := d.AllowAt(env, now)
	if !allowed {
		t.Fatal("first event should pass")
	}

	// Within window: should suppress
	allowed, merged := d.AllowAt(env, now.Add(5*time.Second))
	if allowed {
		t.Fatal("within window should suppress")
	}
	if merged == nil || merged.GenericMessage.DedupCount != 2 {
		t.Fatalf("expected dedup count 2, got %#v", merged)
	}

	// After window: should allow again (must exceed last-seen + window;
	// last-seen was at now+5s, window is 10s, so now+16s is past the boundary)
	allowed, merged = d.AllowAt(env, now.Add(16*time.Second))
	if !allowed || merged != nil {
		t.Fatal("after window expires, event should pass as new")
	}
}

func TestDeduperDifferentMessagesNotMerged(t *testing.T) {
	d := NewDeduper(15 * time.Second)
	now := time.Unix(1000, 0)

	env1 := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "claude_hook",
		GenericMessage: &GenericMessagePayload{
			Title:   "Claude finished",
			Body:    "Task A done",
			Urgency: 0,
		},
	}
	env2 := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "claude_hook",
		GenericMessage: &GenericMessagePayload{
			Title:   "Claude finished",
			Body:    "Task B done",
			Urgency: 0,
		},
	}

	allowed1, _ := d.AllowAt(env1, now)
	allowed2, _ := d.AllowAt(env2, now.Add(1*time.Second))
	if !allowed1 || !allowed2 {
		t.Fatal("different bodies should not be deduped against each other")
	}
}

func TestDeduperNilGenericMessagePassesThrough(t *testing.T) {
	d := NewDeduper(15 * time.Second)
	now := time.Unix(1000, 0)

	env := NotifyEnvelope{
		Kind:   KindImageTransfer,
		Source: "clipboard",
		ImageTransfer: &ImageTransferPayload{
			SessionID: "abc",
			Seq:       1,
			Format:    "png",
		},
	}

	allowed, merged := d.AllowAt(env, now)
	if !allowed || merged != nil {
		t.Fatal("envelope without GenericMessage should always pass")
	}

	// Second identical should also pass (no GenericMessage to dedup on)
	allowed, merged = d.AllowAt(env, now.Add(1*time.Second))
	if !allowed || merged != nil {
		t.Fatal("envelope without GenericMessage should always pass, even repeated")
	}
}

func TestDeduperCountIncrementsCorrectly(t *testing.T) {
	d := NewDeduper(30 * time.Second)
	now := time.Unix(1000, 0)

	env := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "claude_hook",
		GenericMessage: &GenericMessagePayload{
			Title:   "Claude finished",
			Body:    "Done",
			Urgency: 0,
		},
	}

	d.AllowAt(env, now) // count=1, allowed

	_, merged := d.AllowAt(env, now.Add(1*time.Second)) // count=2, suppressed
	if merged == nil || merged.GenericMessage.DedupCount != 2 {
		t.Fatalf("expected dedup count 2, got %v", merged)
	}

	_, merged = d.AllowAt(env, now.Add(2*time.Second)) // count=3, suppressed
	if merged == nil || merged.GenericMessage.DedupCount != 3 {
		t.Fatalf("expected dedup count 3, got %v", merged)
	}

	_, merged = d.AllowAt(env, now.Add(3*time.Second)) // count=4, suppressed
	if merged == nil || merged.GenericMessage.DedupCount != 4 {
		t.Fatalf("expected dedup count 4, got %v", merged)
	}
}

func TestDedupType(t *testing.T) {
	tests := []struct {
		name string
		env  NotifyEnvelope
		want string
	}{
		{
			name: "notification with notif type",
			env: NotifyEnvelope{
				Kind: KindToolAttention,
				ToolAttention: &ToolAttentionPayload{
					HookType:  "notification",
					NotifType: "permission_prompt",
				},
			},
			want: "notification:permission_prompt",
		},
		{
			name: "stop with reason",
			env: NotifyEnvelope{
				Kind: KindToolAttention,
				ToolAttention: &ToolAttentionPayload{
					HookType:   "stop",
					StopReason: "stop_at_end_of_turn",
				},
			},
			want: "stop:stop_at_end_of_turn",
		},
		{
			name: "generic message without tool attention",
			env: NotifyEnvelope{
				Kind: KindGenericMessage,
			},
			want: "generic_message",
		},
		{
			name: "image transfer",
			env: NotifyEnvelope{
				Kind: KindImageTransfer,
			},
			want: "image_transfer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupType(tt.env)
			if got != tt.want {
				t.Fatalf("dedupType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsAlwaysCritical(t *testing.T) {
	tests := []struct {
		name string
		env  NotifyEnvelope
		want bool
	}{
		{
			name: "urgency 2 is critical",
			env: NotifyEnvelope{
				GenericMessage: &GenericMessagePayload{Urgency: 2},
			},
			want: true,
		},
		{
			name: "urgency 1 is not critical",
			env: NotifyEnvelope{
				GenericMessage: &GenericMessagePayload{Urgency: 1},
			},
			want: false,
		},
		{
			name: "nil GenericMessage is not critical",
			env:  NotifyEnvelope{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlwaysCritical(tt.env)
			if got != tt.want {
				t.Fatalf("isAlwaysCritical() = %v, want %v", got, tt.want)
			}
		})
	}
}
