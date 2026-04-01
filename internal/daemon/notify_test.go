package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

type countingNotifier struct {
	count atomic.Int64
	last  atomic.Value // stores NotifyEvent
}

func (n *countingNotifier) Notify(_ context.Context, evt NotifyEvent) error {
	n.count.Add(1)
	n.last.Store(evt)
	return nil
}

func newTestServerWithNotifier(clip ClipboardReader) (*Server, string, *countingNotifier) {
	tm := token.NewManager(1 * time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	notifier := &countingNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.RunNotifier(ctx, notifier)
	_ = cancel // caller is responsible; tests are short-lived
	return srv, s.Token, notifier
}

func TestNotificationTriggeredOnImageFetch(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: fakeImage,
	}
	srv, tok, notifier := newTestServerWithNotifier(clip)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	req.Header.Set("X-CC-Clip-Session", "test-session-abc")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Wait for async notification delivery
	deadline := time.Now().Add(2 * time.Second)
	for notifier.count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if notifier.count.Load() != 1 {
		t.Fatalf("expected 1 notification, got %d", notifier.count.Load())
	}

	evt := notifier.last.Load().(NotifyEvent)
	if evt.SessionID != "test-session-abc" {
		t.Errorf("expected session test-session-abc, got %s", evt.SessionID)
	}
	if evt.Seq != 1 {
		t.Errorf("expected seq 1, got %d", evt.Seq)
	}
	if evt.Format != "png" {
		t.Errorf("expected format png, got %s", evt.Format)
	}
}

func TestNoNotificationWithoutSessionHeader(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47}
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: fakeImage,
	}
	srv, tok, notifier := newTestServerWithNotifier(clip)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	// No X-CC-Clip-Session header
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	time.Sleep(100 * time.Millisecond)
	if notifier.count.Load() != 0 {
		t.Fatalf("expected 0 notifications without session header, got %d", notifier.count.Load())
	}
}

func TestNotificationChannelFullDoesNotBlock(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47}
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: fakeImage,
	}

	tm := token.NewManager(1 * time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	// Do NOT start RunNotifier — channel will fill up

	// Fill the channel
	for i := 0; i < notifyChCap+5; i++ {
		req := httptest.NewRequest("GET", "/clipboard/image", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token)
		req.Header.Set("User-Agent", "cc-clip/0.1")
		req.Header.Set("X-CC-Clip-Session", "fill-test")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
	}
	// If we get here, no deadlock — channel full was handled gracefully
}

func TestImageFetchProducesImageTransferEnvelope(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47}
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: fakeImage,
	}

	tm := token.NewManager(time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	deliverer := &recordingDeliverer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunNotifier(ctx, deliverer)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	req.Header.Set("X-CC-Clip-Session", "session-a")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	deadline := time.Now().Add(2 * time.Second)
	for deliverer.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if deliverer.Count() != 1 {
		t.Fatalf("expected 1 delivery, got %d", deliverer.Count())
	}
	env := deliverer.Last()
	if env.Kind != KindImageTransfer {
		t.Fatalf("expected kind %q, got %q", KindImageTransfer, env.Kind)
	}
	if env.ImageTransfer == nil {
		t.Fatal("expected non-nil ImageTransfer payload")
	}
	if env.ImageTransfer.Format != "png" {
		t.Fatalf("expected format png, got %s", env.ImageTransfer.Format)
	}
	if env.ImageTransfer.SessionID != "session-a" {
		t.Fatalf("expected session-a, got %s", env.ImageTransfer.SessionID)
	}
	if env.ImageTransfer.Seq != 1 {
		t.Fatalf("expected seq 1, got %d", env.ImageTransfer.Seq)
	}
	if env.Source != "clipboard" {
		t.Fatalf("expected source clipboard, got %s", env.Source)
	}
	if env.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

// recordingDeliverer is a test helper that bridges NotifyEvent into
// NotifyEnvelope using newImageTransferEnvelope, then records the result.
// It satisfies the Notifier interface (accepts NotifyEvent) and proves that
// the bridge function produces correct envelopes.
type recordingDeliverer struct {
	count atomic.Int64
	last  atomic.Value // stores NotifyEnvelope
}

func (d *recordingDeliverer) Notify(_ context.Context, evt NotifyEvent) error {
	env := newImageTransferEnvelope("clipboard", ImageTransferPayload{
		SessionID:   evt.SessionID,
		Seq:         evt.Seq,
		Fingerprint: evt.Fingerprint,
		ImageData:   evt.ImageData,
		Format:      evt.Format,
		Width:       evt.Width,
		Height:      evt.Height,
		DuplicateOf: evt.DuplicateOf,
	})
	d.count.Add(1)
	d.last.Store(env)
	return nil
}

func (d *recordingDeliverer) Count() int64 {
	return d.count.Load()
}

func (d *recordingDeliverer) Last() NotifyEnvelope {
	v := d.last.Load()
	if v == nil {
		return NotifyEnvelope{}
	}
	return v.(NotifyEnvelope)
}

type hybridNotifier struct {
	deliverCount atomic.Int64
	notifyCount  atomic.Int64
	lastEnvelope atomic.Value // stores NotifyEnvelope
	lastEvent    atomic.Value // stores NotifyEvent
}

func (n *hybridNotifier) Deliver(_ context.Context, env NotifyEnvelope) error {
	n.deliverCount.Add(1)
	n.lastEnvelope.Store(env)
	return nil
}

func (n *hybridNotifier) Notify(_ context.Context, evt NotifyEvent) error {
	n.notifyCount.Add(1)
	n.lastEvent.Store(evt)
	return nil
}

func TestCriticalEnvelopeRoutedToCriticalChannel(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-crit")

	// Send a permission_prompt (urgency=2 => critical)
	body := `{"hook_event_name":"Notification","type":"permission_prompt","title":"Approve tool","body":"Edit file","_cc_clip_host":"mars"}`
	req := httptest.NewRequest("POST", "/notify", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer nonce-crit")
	req.Header.Set("Content-Type", "application/x-claude-hook")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Critical envelope should be in criticalCh
	select {
	case env := <-srv.criticalCh:
		if env.GenericMessage == nil || env.GenericMessage.Urgency != 2 {
			t.Fatalf("expected urgency 2, got %+v", env.GenericMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("expected critical envelope in criticalCh, timed out")
	}
}

func TestNonCriticalEnvelopeRoutedToNotifyChannel(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-norm")

	// Send a generic (non-critical) notification
	body := `{"title":"Info","body":"Build passed","urgency":0}`
	req := httptest.NewRequest("POST", "/notify", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer nonce-norm")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Non-critical should be in notifyCh
	select {
	case env := <-srv.notifyCh:
		if env.Kind != KindGenericMessage {
			t.Fatalf("expected kind %q, got %q", KindGenericMessage, env.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("expected envelope in notifyCh, timed out")
	}
}

func TestNotifyNonCriticalChannelFullDoesNotBlock(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-full")

	// Fill the notifyCh entirely (no RunNotifier consuming)
	for i := 0; i < notifyChCap+10; i++ {
		body := `{"title":"Flood","body":"msg","urgency":0}`
		req := httptest.NewRequest("POST", "/notify", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer nonce-full")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("request %d: expected 204, got %d", i, w.Code)
		}
	}
	// If we get here, no deadlock — non-critical full channel was handled
}

func TestRunNotifierDrainsBothChannels(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	notifier := &countingNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunNotifier(ctx, notifier)

	// Enqueue one via notifyCh (image transfer style)
	srv.enqueueEnvelope(newImageTransferEnvelope("clipboard", ImageTransferPayload{
		SessionID: "s1",
		Seq:       1,
		Format:    "png",
	}))

	// Enqueue one via criticalCh (permission_prompt)
	critEnv := NotifyEnvelope{
		Kind:   KindGenericMessage,
		Source: "claude_hook",
		GenericMessage: &GenericMessagePayload{
			Title:   "Tool approval needed",
			Body:    "Edit file",
			Urgency: 2,
		},
	}
	srv.enqueueEnvelope(critEnv)

	// Wait for both to be consumed
	deadline := time.Now().Add(2 * time.Second)
	for notifier.count.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if notifier.count.Load() != 2 {
		t.Fatalf("expected 2 notifications, got %d", notifier.count.Load())
	}
}

func TestRunNotifierUsesEnvelopeDeliveryWhenAvailable(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	notifier := &hybridNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunNotifier(ctx, notifier)

	srv.enqueueEnvelope(NotifyEnvelope{
		Kind:      KindGenericMessage,
		Source:    "cli",
		Timestamp: time.Now().UTC(),
		GenericMessage: &GenericMessagePayload{
			Title:   "Build complete",
			Body:    "All tests passed",
			Urgency: 1,
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for notifier.deliverCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if notifier.deliverCount.Load() != 1 {
		t.Fatalf("expected Deliver to be used once, got %d", notifier.deliverCount.Load())
	}
	if notifier.notifyCount.Load() != 0 {
		t.Fatalf("expected legacy Notify path to be skipped, got %d", notifier.notifyCount.Load())
	}

	got := notifier.lastEnvelope.Load()
	if got == nil {
		t.Fatal("expected delivered envelope")
	}
	env := got.(NotifyEnvelope)
	if env.GenericMessage == nil {
		t.Fatal("expected GenericMessage payload")
	}
	if env.GenericMessage.Title != "Build complete" {
		t.Fatalf("expected title preserved, got %q", env.GenericMessage.Title)
	}
	if env.GenericMessage.Body != "All tests passed" {
		t.Fatalf("expected body preserved, got %q", env.GenericMessage.Body)
	}
}

func TestDuplicateDetectionViaNotification(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: fakeImage,
	}
	srv, tok, notifier := newTestServerWithNotifier(clip)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/clipboard/image", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("User-Agent", "cc-clip/0.1")
		req.Header.Set("X-CC-Clip-Session", "dup-test")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
	}

	deadline := time.Now().Add(2 * time.Second)
	for notifier.count.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if notifier.count.Load() != 2 {
		t.Fatalf("expected 2 notifications, got %d", notifier.count.Load())
	}

	evt := notifier.last.Load().(NotifyEvent)
	if evt.Seq != 2 {
		t.Errorf("expected seq 2, got %d", evt.Seq)
	}
	if evt.DuplicateOf != 1 {
		t.Errorf("expected duplicate of seq 1, got %d", evt.DuplicateOf)
	}
}
