package daemon

import (
	"context"
	"errors"
	"testing"
)

// fakeDeliverer is a test double for the Deliverer interface.
type fakeDeliverer struct {
	name  string
	err   error
	calls int
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ NotifyEnvelope) error {
	f.calls++
	return f.err
}

func (f *fakeDeliverer) Name() string { return f.name }

func TestDeliveryChainFallsBackToSecondAdapter(t *testing.T) {
	first := &fakeDeliverer{name: "cmux", err: errors.New("cmux unavailable")}
	second := &fakeDeliverer{name: "darwin"}
	chain := &DeliveryChain{adapters: []Deliverer{first, second}}

	err := chain.Deliver(context.Background(), NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "cli",
		GenericMessage: &GenericMessagePayload{
			Title: "Build complete",
			Body:  "ok",
		},
	})
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("expected both adapters to run, got first=%d second=%d", first.calls, second.calls)
	}
}

func TestDeliveryChainStopsOnFirstSuccess(t *testing.T) {
	first := &fakeDeliverer{name: "cmux"}
	second := &fakeDeliverer{name: "darwin"}
	chain := &DeliveryChain{adapters: []Deliverer{first, second}}

	err := chain.Deliver(context.Background(), NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "cli",
		GenericMessage: &GenericMessagePayload{
			Title: "Test",
			Body:  "ok",
		},
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if first.calls != 1 {
		t.Fatalf("expected first adapter called once, got %d", first.calls)
	}
	if second.calls != 0 {
		t.Fatalf("expected second adapter not called, got %d", second.calls)
	}
}

func TestDeliveryChainAllFail(t *testing.T) {
	first := &fakeDeliverer{name: "cmux", err: errors.New("cmux down")}
	second := &fakeDeliverer{name: "darwin", err: errors.New("darwin down")}
	chain := &DeliveryChain{adapters: []Deliverer{first, second}}

	err := chain.Deliver(context.Background(), NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "cli",
		GenericMessage: &GenericMessagePayload{
			Title: "Fail",
			Body:  "all",
		},
	})
	if err == nil {
		t.Fatal("expected error when all adapters fail")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("expected both adapters called, got first=%d second=%d", first.calls, second.calls)
	}
}

func TestDeliveryChainNoAdapters(t *testing.T) {
	chain := &DeliveryChain{adapters: []Deliverer{}}

	err := chain.Deliver(context.Background(), NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "cli",
		GenericMessage: &GenericMessagePayload{
			Title: "Empty",
			Body:  "chain",
		},
	})
	if err == nil {
		t.Fatal("expected error with no adapters")
	}
}

func TestFormatNotificationImageTransfer(t *testing.T) {
	env := NotifyEnvelope{
		Kind:   KindImageTransfer,
		Source: "clipboard",
		ImageTransfer: &ImageTransferPayload{
			Seq:         3,
			Fingerprint: "abc12345",
			Width:       800,
			Height:      600,
			Format:      "png",
			DuplicateOf: 0,
		},
	}
	title, body := formatNotification(env)
	if title != "cc-clip #3" {
		t.Fatalf("expected title 'cc-clip #3', got %q", title)
	}
	if body != "abc12345 800x600 png" {
		t.Fatalf("expected body 'abc12345 800x600 png', got %q", body)
	}
}

func TestFormatNotificationImageTransferDuplicate(t *testing.T) {
	env := NotifyEnvelope{
		Kind:   KindImageTransfer,
		Source: "clipboard",
		ImageTransfer: &ImageTransferPayload{
			Seq:         5,
			Fingerprint: "def67890",
			Width:       1920,
			Height:      1080,
			Format:      "jpeg",
			DuplicateOf: 2,
		},
	}
	title, body := formatNotification(env)
	if title != "cc-clip #5" {
		t.Fatalf("expected title 'cc-clip #5', got %q", title)
	}
	if body != "Duplicate of #2" {
		t.Fatalf("expected body 'Duplicate of #2', got %q", body)
	}
}

func TestFormatNotificationGenericMessage(t *testing.T) {
	env := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "cli",
		GenericMessage: &GenericMessagePayload{
			Title:    "Build complete",
			Body:     "All tests passed",
			Verified: true,
		},
	}
	title, body := formatNotification(env)
	if title != "Build complete" {
		t.Fatalf("expected title 'Build complete', got %q", title)
	}
	if body != "All tests passed" {
		t.Fatalf("expected body 'All tests passed', got %q", body)
	}
}

func TestFormatNotificationGenericMessageUnverified(t *testing.T) {
	env := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "cli",
		GenericMessage: &GenericMessagePayload{
			Title: "Build complete",
			Body:  "All tests passed",
		},
	}
	title, body := formatNotification(env)
	if title != "[unverified] Build complete" {
		t.Fatalf("expected unverified title, got %q", title)
	}
	if body != "All tests passed" {
		t.Fatalf("expected body 'All tests passed', got %q", body)
	}
}

func TestFormatNotificationToolAttention(t *testing.T) {
	env := NotifyEnvelope{
		Kind:   KindToolAttention,
		Source: "claude_hook",
		ToolAttention: &ToolAttentionPayload{
			HookType:   "notification",
			NotifType:  "permission_prompt",
			ToolName:   "Bash",
			ToolInput:  "rm -rf /",
			StopReason: "",
		},
		GenericMessage: &GenericMessagePayload{
			Title: "Tool approval needed",
			Body:  "Bash wants to run",
		},
	}
	title, body := formatNotification(env)
	if title != "Tool approval needed" {
		t.Fatalf("expected title 'Tool approval needed', got %q", title)
	}
	if body != "Bash wants to run" {
		t.Fatalf("expected body 'Bash wants to run', got %q", body)
	}
}

func TestDeliveryChainNotifyBridgesEvent(t *testing.T) {
	recorder := &fakeDeliverer{name: "test"}
	chain := &DeliveryChain{adapters: []Deliverer{recorder}}

	evt := NotifyEvent{
		SessionID:   "sess-123",
		Seq:         7,
		Fingerprint: "aabbccdd",
		Format:      "png",
		Width:       640,
		Height:      480,
	}
	err := chain.Notify(context.Background(), evt)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if recorder.calls != 1 {
		t.Fatalf("expected 1 call, got %d", recorder.calls)
	}
}
