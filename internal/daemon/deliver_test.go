package daemon

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestWarnIfNoAdaptersLogsWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	t.Run("empty chain logs a warning", func(t *testing.T) {
		buf.Reset()
		warnIfNoAdapters(nil)
		out := buf.String()
		if !strings.Contains(out, "WARN") {
			t.Fatalf("expected a WARNING when no adapters are available, got %q", out)
		}
	})

	t.Run("non-empty chain logs nothing", func(t *testing.T) {
		buf.Reset()
		warnIfNoAdapters([]Deliverer{&fakeDeliverer{name: "x"}})
		if out := buf.String(); out != "" {
			t.Fatalf("expected no log output when adapters exist, got %q", out)
		}
	})
}

func TestCmuxDelivererHonorsCanceledContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	dir := t.TempDir()
	cmux := filepath.Join(dir, "cmux")
	if err := os.WriteFile(cmux, []byte("#!/bin/sh\nsleep 0.3\n"), 0755); err != nil {
		t.Fatalf("write fake cmux: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := (&CmuxDeliverer{path: cmux}).Deliver(ctx, NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "test",
		GenericMessage: &GenericMessagePayload{
			Title: "cancelled",
			Body:  "should not run",
		},
	})
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("Deliver ignored canceled context; elapsed=%v", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// fakeCtxCanceledDeliverer always returns context.Canceled, simulating
// an adapter that observed cancellation mid-flight.
type fakeCtxCanceledDeliverer struct {
	name  string
	calls int
}

func (f *fakeCtxCanceledDeliverer) Deliver(_ context.Context, _ NotifyEnvelope) error {
	f.calls++
	return context.Canceled
}

func (f *fakeCtxCanceledDeliverer) Name() string { return f.name }

func TestDeliveryChainShortCircuitsOnContextCanceled(t *testing.T) {
	// If the first adapter returns context.Canceled, the chain must
	// surface it and NOT fall through to the next adapter. Falling
	// through would silently override the caller's intent to abort.
	first := &fakeCtxCanceledDeliverer{name: "cmux"}
	second := &fakeDeliverer{name: "darwin"} // would succeed if called
	chain := &DeliveryChain{adapters: []Deliverer{first, second}}

	err := chain.Deliver(context.Background(), NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "test",
		GenericMessage: &GenericMessagePayload{
			Title: "cancelled",
			Body:  "should propagate",
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected chain to propagate context.Canceled, got %v", err)
	}
	if first.calls != 1 {
		t.Fatalf("expected first adapter to be called once, got %d", first.calls)
	}
	if second.calls != 0 {
		t.Fatalf("expected fallback NOT to run after cancellation, got %d calls", second.calls)
	}
}

func TestDeliveryChainBailsBeforeAdapterWhenContextDone(t *testing.T) {
	// If ctx is already canceled before the loop starts, no adapter
	// should be invoked at all — the chain returns the ctx error immediately.
	first := &fakeDeliverer{name: "cmux"}
	chain := &DeliveryChain{adapters: []Deliverer{first}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := chain.Deliver(ctx, NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "test",
		GenericMessage: &GenericMessagePayload{
			Title: "pre-cancelled",
			Body:  "should not run any adapter",
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if first.calls != 0 {
		t.Fatalf("expected zero adapter calls when ctx is pre-canceled, got %d", first.calls)
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

func TestNotificationSoundAllowlistAndCriticalDefault(t *testing.T) {
	// Isolate from the host/CI environment: the delivery sound policy now reads
	// these CC_CLIP_SOUND_* vars, so clear them to assert the built-in defaults.
	t.Setenv("CC_CLIP_SOUND_CRITICAL", "")
	t.Setenv("CC_CLIP_SOUND_ATTENTION", "")
	t.Setenv("CC_CLIP_SOUND_CALM", "")
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 2}}); got != defaultCriticalSound {
		t.Fatalf("critical default sound = %q, want %q", got, defaultCriticalSound)
	}
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 1, Sound: "ping"}}); got != "Ping" {
		t.Fatalf("allowlisted sound = %q, want Ping", got)
	}
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 2, Sound: "not-a-sound"}}); got != "" {
		t.Fatalf("unknown explicit sound should be silent, got %q", got)
	}
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 2, Sound: "none"}}); got != "" {
		t.Fatalf("sound=none should suppress critical default, got %q", got)
	}
}

func TestDefaultSoundPolicyByUrgency(t *testing.T) {
	// With no explicit sound and no env override, only the critical tier
	// (urgency >= 2) makes a sound; attention/calm tiers stay silent so the
	// historical completion/idle UX is unchanged.
	t.Setenv("CC_CLIP_SOUND_CRITICAL", "")
	t.Setenv("CC_CLIP_SOUND_ATTENTION", "")
	t.Setenv("CC_CLIP_SOUND_CALM", "")
	cases := []struct {
		urgency int
		want    string
	}{
		{3, defaultCriticalSound},
		{2, defaultCriticalSound},
		{1, ""},
		{0, ""},
		{-1, ""},
	}
	for _, c := range cases {
		got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: c.urgency}})
		if got != c.want {
			t.Fatalf("urgency %d default sound = %q, want %q", c.urgency, got, c.want)
		}
	}
}

func TestDefaultSoundPolicyEnvOverridesEachTier(t *testing.T) {
	t.Setenv("CC_CLIP_SOUND_CRITICAL", "Sosumi")
	t.Setenv("CC_CLIP_SOUND_ATTENTION", "Pop")
	t.Setenv("CC_CLIP_SOUND_CALM", "Tink")
	cases := []struct {
		urgency int
		want    string
	}{
		{2, "Sosumi"},
		{1, "Pop"},
		{0, "Tink"},
	}
	for _, c := range cases {
		got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: c.urgency}})
		if got != c.want {
			t.Fatalf("urgency %d with env override = %q, want %q", c.urgency, got, c.want)
		}
	}
}

func TestDefaultSoundPolicyEnvCanSilenceCriticalTier(t *testing.T) {
	t.Setenv("CC_CLIP_SOUND_CRITICAL", "off")
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 2}}); got != "" {
		t.Fatalf("CC_CLIP_SOUND_CRITICAL=off should silence the critical default, got %q", got)
	}
}

func TestDefaultSoundPolicyEnvUnknownNameIsSilent(t *testing.T) {
	// An unrecognized env sound name resolves to silence rather than a
	// surprising fallback to the built-in default.
	t.Setenv("CC_CLIP_SOUND_ATTENTION", "not-a-real-sound")
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 1}}); got != "" {
		t.Fatalf("unknown env sound should be silent, got %q", got)
	}
}

func TestNotificationSoundExplicitPayloadBeatsEnvPolicy(t *testing.T) {
	// A per-notification Sound (e.g. from a generic /notify payload) wins over
	// the env tier policy.
	t.Setenv("CC_CLIP_SOUND_ATTENTION", "Pop")
	if got := notificationSound(NotifyEnvelope{GenericMessage: &GenericMessagePayload{Urgency: 1, Sound: "ping"}}); got != "Ping" {
		t.Fatalf("explicit payload sound should win over env policy, got %q", got)
	}
}

func TestNotificationSoundNilGenericMessageIsSilent(t *testing.T) {
	if got := notificationSound(NotifyEnvelope{}); got != "" {
		t.Fatalf("nil generic message should be silent, got %q", got)
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
