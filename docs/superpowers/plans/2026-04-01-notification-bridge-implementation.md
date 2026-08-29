# Notification Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the SSH notification bridge end-to-end so remote Claude Code hooks, Codex notify hooks, and generic `cc-clip notify` calls can deliver local notifications through the existing cc-clip tunnel.

**Architecture:** Keep clipboard transfer notifications working while introducing a new envelope model for all notification traffic. Implement pure logic first (envelope, classifier, dedup), then delivery chaining, then `/notify` server integration, then remote assets and `connect` automation, with tests at each boundary.

**Tech Stack:** Go, net/http, httptest, os/exec, bash templates, existing cc-clip daemon/session/shim infrastructure

---

## File Map

- Create: `internal/daemon/envelope.go`
- Create: `internal/daemon/classifier.go`
- Create: `internal/daemon/classifier_test.go`
- Create: `internal/daemon/dedup.go`
- Create: `internal/daemon/dedup_test.go`
- Create: `internal/daemon/deliver.go`
- Create: `internal/daemon/deliver_cmux.go`
- Create: `internal/daemon/deliver_other.go`
- Create: `internal/shim/hook_template.go`
- Create: `internal/shim/hook_template_test.go`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/notifier.go`
- Modify: `internal/daemon/notify_darwin.go`
- Modify: `internal/daemon/notify_test.go`
- Modify: `internal/daemon/server_test.go`
- Modify: `internal/shim/deploy.go`
- Modify: `cmd/cc-clip/main.go`
- Modify: `cmd/cc-clip/main_test.go`

## Task 1: Introduce the Unified Envelope Model

**Files:**
- Create: `internal/daemon/envelope.go`
- Modify: `internal/daemon/notifier.go`
- Test: `internal/daemon/notify_test.go`

- [ ] **Step 1: Write the failing envelope bridge test**

Add a new test case to `internal/daemon/notify_test.go` that asserts clipboard fetch notifications are bridged into an image-transfer envelope rather than a raw `NotifyEvent`.

```go
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

	require.Eventually(t, func() bool { return deliverer.Count() == 1 }, 2*time.Second, 10*time.Millisecond)
	env := deliverer.Last()
	if env.Kind != KindImageTransfer {
		t.Fatalf("expected %q, got %q", KindImageTransfer, env.Kind)
	}
	if env.ImageTransfer == nil || env.ImageTransfer.Format != "png" {
		t.Fatalf("expected png image payload, got %#v", env.ImageTransfer)
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `go test ./internal/daemon -run TestImageFetchProducesImageTransferEnvelope -count=1`

Expected: build failure because `KindImageTransfer`, `NotifyEnvelope`, and `recordingDeliverer` do not exist yet.

- [ ] **Step 3: Add the envelope types**

Create `internal/daemon/envelope.go` with the shared envelope model.

```go
package daemon

import "time"

type NotifyKind string

const (
	KindImageTransfer NotifyKind = "image_transfer"
	KindToolAttention NotifyKind = "tool_attention"
	KindGenericMessage NotifyKind = "generic_message"
)

type NotifyEnvelope struct {
	Kind      NotifyKind
	Source    string
	Host      string
	Timestamp time.Time

	ImageTransfer  *ImageTransferPayload
	ToolAttention  *ToolAttentionPayload
	GenericMessage *GenericMessagePayload
}

type ImageTransferPayload struct {
	SessionID   string
	Seq         int
	Fingerprint string
	ImageData   []byte
	Format      string
	Width       int
	Height      int
	DuplicateOf int
}

type ToolAttentionPayload struct {
	SessionID  string
	HookType   string
	StopReason string
	NotifType  string
	ToolName   string
	ToolInput  string
	Message    string
	Verified   bool
}

type GenericMessagePayload struct {
	Title      string
	Body       string
	Urgency    int
	Verified   bool
	Subtitle   string
	DedupCount int
}
```

- [ ] **Step 4: Bridge legacy image notifications into envelopes**

Update `internal/daemon/notifier.go` so notifier consumers operate on envelopes, and add a helper that converts existing clipboard transfer metadata into `NotifyEnvelope`.

```go
package daemon

import (
	"context"
	"time"
)

type Notifier interface {
	Notify(ctx context.Context, event NotifyEnvelope) error
}

type NopNotifier struct{}

func (NopNotifier) Notify(context.Context, NotifyEnvelope) error { return nil }

func newImageTransferEnvelope(source string, payload ImageTransferPayload) NotifyEnvelope {
	return NotifyEnvelope{
		Kind:      KindImageTransfer,
		Source:    source,
		Timestamp: time.Now().UTC(),
		ImageTransfer: &ImageTransferPayload{
			SessionID:   payload.SessionID,
			Seq:         payload.Seq,
			Fingerprint: payload.Fingerprint,
			ImageData:   payload.ImageData,
			Format:      payload.Format,
			Width:       payload.Width,
			Height:      payload.Height,
			DuplicateOf: payload.DuplicateOf,
		},
	}
}
```

- [ ] **Step 5: Add the test helper used by daemon notification tests**

Update `internal/daemon/notify_test.go` with a simple deliverer stub.

```go
type recordingDeliverer struct {
	count atomic.Int64
	last  atomic.Value // stores NotifyEnvelope
}

func (d *recordingDeliverer) Notify(_ context.Context, env NotifyEnvelope) error {
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
```

- [ ] **Step 6: Re-run daemon notification tests**

Run: `go test ./internal/daemon -run 'Test(ImageFetchProducesImageTransferEnvelope|NotificationTriggeredOnImageFetch|DuplicateDetectionViaNotification)' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/daemon/envelope.go internal/daemon/notifier.go internal/daemon/notify_test.go
git commit -m "feat: add notification envelope model"
```

## Task 2: Build the Hook Classifier and Dedup Engine

**Files:**
- Create: `internal/daemon/classifier.go`
- Create: `internal/daemon/classifier_test.go`
- Create: `internal/daemon/dedup.go`
- Create: `internal/daemon/dedup_test.go`

- [ ] **Step 1: Write the failing classifier tests**

Create `internal/daemon/classifier_test.go`.

```go
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
```

- [ ] **Step 2: Write the failing dedup tests**

Create `internal/daemon/dedup_test.go`.

```go
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
```

- [ ] **Step 3: Run classifier and dedup tests**

Run: `go test ./internal/daemon -run 'Test(ClassifyHookPayload|Deduper)' -count=1`

Expected: build failure because classifier and deduper do not exist yet.

- [ ] **Step 4: Implement the classifier**

Create `internal/daemon/classifier.go`.

```go
package daemon

import (
	"fmt"
	"strings"
	"time"
)

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

func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "…"
}
```

- [ ] **Step 5: Implement the dedup engine**

Create `internal/daemon/dedup.go`.

```go
package daemon

import (
	"crypto/md5"
	"sync"
	"time"
)

type DedupKey struct {
	Source   string
	Type     string
	Title    string
	BodyHash [16]byte
}

type DedupEntry struct {
	SeenAt time.Time
	Count  int
}

type Deduper struct {
	mu     sync.Mutex
	window time.Duration
	items  map[DedupKey]DedupEntry
}

func NewDeduper(window time.Duration) *Deduper {
	return &Deduper{window: window, items: make(map[DedupKey]DedupEntry)}
}

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
```

- [ ] **Step 6: Re-run the pure logic tests**

Run: `go test ./internal/daemon -run 'Test(ClassifyHookPayload|Deduper)' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/daemon/classifier.go internal/daemon/classifier_test.go internal/daemon/dedup.go internal/daemon/dedup_test.go
git commit -m "feat: add hook classification and dedup logic"
```

## Task 3: Add Delivery Chaining and Preserve macOS Fallback

**Files:**
- Create: `internal/daemon/deliver.go`
- Create: `internal/daemon/deliver_cmux.go`
- Create: `internal/daemon/deliver_other.go`
- Modify: `internal/daemon/notify_darwin.go`

- [ ] **Step 1: Write the failing delivery chain tests**

Add `internal/daemon/deliver_test.go`.

```go
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
```

- [ ] **Step 2: Run the delivery test and verify it fails**

Run: `go test ./internal/daemon -run TestDeliveryChainFallsBackToSecondAdapter -count=1`

Expected: build failure because `DeliveryChain` and `Deliverer` do not exist yet.

- [ ] **Step 3: Implement the delivery chain**

Create `internal/daemon/deliver.go`.

```go
package daemon

import (
	"context"
	"fmt"
	"log"
)

type Deliverer interface {
	Deliver(ctx context.Context, env NotifyEnvelope) error
	Name() string
}

type DeliveryChain struct {
	adapters []Deliverer
}

func (c *DeliveryChain) Notify(ctx context.Context, env NotifyEnvelope) error {
	var lastErr error
	for _, adapter := range c.adapters {
		if err := adapter.Deliver(ctx, env); err != nil {
			lastErr = err
			log.Printf("delivery adapter %s failed: %v", adapter.Name(), err)
			continue
		}
		return nil
	}
	if lastErr == nil {
		return fmt.Errorf("no delivery adapters configured")
	}
	return lastErr
}

func BuildDeliveryChain() *DeliveryChain {
	adapters := make([]Deliverer, 0, 2)
	if cmux := NewCmuxDeliverer(); cmux != nil {
		adapters = append(adapters, cmux)
	}
	if darwin := NewDarwinDeliverer(); darwin != nil {
		adapters = append(adapters, darwin)
	}
	return &DeliveryChain{adapters: adapters}
}
```

- [ ] **Step 4: Add the cmux and no-op deliverers**

Create `internal/daemon/deliver_cmux.go` and `internal/daemon/deliver_other.go`.

```go
package daemon

import (
	"context"
	"fmt"
	"os/exec"
)

type CmuxDeliverer struct {
	path string
}

func NewCmuxDeliverer() *CmuxDeliverer {
	path, err := exec.LookPath("cmux")
	if err != nil {
		return nil
	}
	return &CmuxDeliverer{path: path}
}

func (d *CmuxDeliverer) Name() string { return "cmux" }

func (d *CmuxDeliverer) Deliver(_ context.Context, env NotifyEnvelope) error {
	title, body := formatNotification(env)
	cmd := exec.Command(d.path, "notify", "--title", title, "--body", body)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cmux notify failed: %s: %w", string(out), err)
	}
	return nil
}
```

- [ ] **Step 5: Convert the existing darwin notifier into a deliverer**

Update `internal/daemon/notify_darwin.go` so it exposes `NewDarwinDeliverer()` and formats both image and generic notifications.

```go
func NewDarwinDeliverer() *DarwinNotifier {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cache", "cc-clip", "previews")
	_ = os.MkdirAll(dir, 0700)
	tn, _ := exec.LookPath("terminal-notifier")
	return &DarwinNotifier{previewDir: dir, terminalNotifier: tn}
}

func (n *DarwinNotifier) Name() string { return "darwin" }

func (n *DarwinNotifier) Deliver(_ context.Context, env NotifyEnvelope) error {
	title, subtitle, body, imagePath := n.render(env)
	if n.terminalNotifier != "" {
		return n.sendViaTerminalNotifier(title, subtitle, body, imagePath)
	}
	return n.sendViaOsascript(title, subtitle, body)
}
```

- [ ] **Step 6: Re-run delivery tests**

Run: `go test ./internal/daemon -run 'TestDeliveryChainFallsBackToSecondAdapter|TestNotificationTriggeredOnImageFetch' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/daemon/deliver.go internal/daemon/deliver_cmux.go internal/daemon/deliver_other.go internal/daemon/notify_darwin.go internal/daemon/deliver_test.go
git commit -m "feat: add chained notification delivery"
```

## Task 4: Integrate `/notify` and Dual Queues in the Daemon

**Files:**
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/server_test.go`
- Modify: `internal/daemon/notify_test.go`

- [ ] **Step 1: Write failing `/notify` endpoint tests**

Add tests to `internal/daemon/server_test.go`.

```go
func TestNotifyEndpointAcceptsClaudeHookPayload(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-123")

	body := strings.NewReader(`{"hook_event_name":"Notification","type":"permission_prompt","title":"Approve tool","body":"Claude wants to Edit file","_cc_clip_host":"venus"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-123")
	req.Header.Set("Content-Type", "application/x-claude-hook")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run the daemon server tests and verify failure**

Run: `go test ./internal/daemon -run 'TestNotifyEndpointAcceptsClaudeHookPayload|TestNotificationChannelFullDoesNotBlock' -count=1`

Expected: build failure because `RegisterNotificationNonce` and `/notify` do not exist.

- [ ] **Step 3: Add the dedicated nonce registry and dual queues**

Update `internal/daemon/server.go`.

```go
type Server struct {
	clipboard    ClipboardReader
	tokens       *token.Manager
	sessions     *session.Store
	notifyCh     chan NotifyEnvelope
	criticalCh   chan NotifyEnvelope
	notifyNonces map[string]struct{}
	noncesMu     sync.RWMutex
	addr         string
	mux          *http.ServeMux
}

func (s *Server) RegisterNotificationNonce(nonce string) {
	s.noncesMu.Lock()
	defer s.noncesMu.Unlock()
	s.notifyNonces[nonce] = struct{}{}
}
```

- [ ] **Step 4: Add the `/notify` handler and enqueue rules**

In `internal/daemon/server.go`, register the new route and implement enqueue helpers.

```go
s.mux.HandleFunc("POST /notify", s.handleNotify)

func (s *Server) enqueueEnvelope(env NotifyEnvelope) {
	if isAlwaysCritical(env) {
		s.criticalCh <- env
		return
	}
	select {
	case s.notifyCh <- env:
	default:
	}
}

func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	nonce := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !s.validNotificationNonce(nonce) {
		http.Error(w, "invalid notification nonce", http.StatusUnauthorized)
		return
	}

	env, err := s.parseNotifyRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.enqueueEnvelope(env)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Convert image-fetch notifications to envelope enqueue**

Replace the existing `NotifyEvent` construction in `handleClipboardImage` with `newImageTransferEnvelope(...)` and route critical vs non-critical queueing through `enqueueEnvelope`.

- [ ] **Step 6: Re-run daemon integration tests**

Run: `go test ./internal/daemon -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/daemon/server.go internal/daemon/server_test.go internal/daemon/notify_test.go
git commit -m "feat: add notify endpoint and queueing"
```

## Task 5: Add Remote Hook Assets and the `notify` Subcommand

**Files:**
- Create: `internal/shim/hook_template.go`
- Create: `internal/shim/hook_template_test.go`
- Modify: `cmd/cc-clip/main.go`

- [ ] **Step 1: Write the failing hook template test**

Create `internal/shim/hook_template_test.go`.

```go
func TestHookTemplateUsesNotificationNonceAndHealthLog(t *testing.T) {
	got := HookScript(18339)
	for _, needle := range []string{
		"notify.nonce",
		"notify-health.log",
		"application/x-claude-hook",
		"Authorization: Bearer $_nonce",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected template to contain %q", needle)
		}
	}
}
```

- [ ] **Step 2: Write the failing CLI notify test**

Add a main-command test that exercises the new parser.

```go
func TestNotifyFromCodexParsesLastAssistantMessage(t *testing.T) {
	payload := `{"last-assistant-message":"Bridge implementation complete"}`
	msg, err := parseCodexNotifyPayload(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Body != "Bridge implementation complete" {
		t.Fatalf("unexpected body %q", msg.Body)
	}
}
```

- [ ] **Step 3: Run the focused tests and verify failure**

Run: `go test ./internal/shim ./cmd/cc-clip -run 'Test(HookTemplateUsesNotificationNonceAndHealthLog|NotifyFromCodexParsesLastAssistantMessage)' -count=1`

Expected: build failure because `HookScript` and `parseCodexNotifyPayload` do not exist.

- [ ] **Step 4: Add the hook template**

Create `internal/shim/hook_template.go`.

```go
package shim

import "fmt"

const hookTemplate = `#!/usr/bin/env bash
set -euo pipefail

_CC_CLIP_PORT="${CC_CLIP_PORT:-%d}"
_CC_CLIP_NONCE_FILE="${HOME}/.cache/cc-clip/notify.nonce"
_CC_CLIP_HOST_ALIAS="${CC_CLIP_HOST_ALIAS:-$(hostname -s)}"
_CC_CLIP_HEALTH_FILE="${HOME}/.cache/cc-clip/notify-health.log"

_nonce=""
if [ -f "$_CC_CLIP_NONCE_FILE" ]; then
	_nonce=$(head -1 "$_CC_CLIP_NONCE_FILE")
fi

_payload=$(cat)

_payload=$(echo "$_payload" | python3 -c "
import sys, json
d = json.load(sys.stdin)
d['_cc_clip_host'] = '${_CC_CLIP_HOST_ALIAS}'
json.dump(d, sys.stdout)
" 2>/dev/null || echo "$_payload")

_http_code=$(curl -sf -o /dev/null -w '%%{http_code}' -X POST \
	-H "Authorization: Bearer $_nonce" \
	-H "Content-Type: application/x-claude-hook" \
	-H "User-Agent: cc-clip-hook/0.1" \
	-d "$_payload" \
	"http://127.0.0.1:${_CC_CLIP_PORT}/notify" \
	2>/dev/null) || _http_code="000"

if [ "$_http_code" != "204" ] && [ "$_http_code" != "200" ]; then
	echo "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) FAIL http=$_http_code" >> "$_CC_CLIP_HEALTH_FILE" 2>/dev/null || true
fi

exit 0
`

func HookScript(port int) string {
	return fmt.Sprintf(hookTemplate, port)
}
```

- [ ] **Step 5: Add the `notify` subcommand parser and HTTP client path**

Update `cmd/cc-clip/main.go`.

```go
case "notify":
	cmdNotify()

func cmdNotify() {
	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	title := fs.String("title", "", "notification title")
	body := fs.String("body", "", "notification body")
	urgency := fs.Int("urgency", 1, "notification urgency")
	fromCodex := fs.String("from-codex", "", "Codex notify JSON payload")
	_ = fs.Parse(os.Args[2:])

	msg := daemon.GenericMessagePayload{
		Title:   *title,
		Body:    *body,
		Urgency: *urgency,
	}
	if *fromCodex != "" {
		parsed, err := parseCodexNotifyPayload(*fromCodex)
		if err != nil {
			log.Fatalf("invalid codex notify payload: %v", err)
		}
		msg = parsed
	}

	if err := postGenericNotification(msg); err != nil {
		log.Fatalf("notify failed: %v", err)
	}
}
```

- [ ] **Step 6: Re-run shim and command tests**

Run: `go test ./internal/shim ./cmd/cc-clip -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/shim/hook_template.go internal/shim/hook_template_test.go cmd/cc-clip/main.go cmd/cc-clip/main_test.go
git commit -m "feat: add remote notification bridge assets"
```

## Task 6: Automate `connect` Setup, Codex Injection, and Health Verification

**Files:**
- Modify: `internal/shim/deploy.go`
- Modify: `internal/shim/ssh.go`
- Modify: `cmd/cc-clip/main.go`
- Modify: `cmd/cc-clip/main_test.go`

- [ ] **Step 1: Write the failing connect-flow tests**

Add tests covering nonce persistence and config injection.

```go
func TestDeployStatePersistsNotificationSetup(t *testing.T) {
	state := &shim.DeployState{
		BinaryHash: "sha256:abc",
		Notify: &shim.NotifyDeployState{
			Enabled:        true,
			HookInstalled:  true,
			CodexInjected:  true,
			HealthVerified: true,
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(raw), `"notify"`) {
		t.Fatalf("expected notify block, got %s", raw)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/shim ./cmd/cc-clip -run 'TestDeployStatePersistsNotificationSetup' -count=1`

Expected: build failure because the deploy state does not include notify metadata yet.

- [ ] **Step 3: Extend deploy state and remote write helpers**

Update `internal/shim/deploy.go` with notification state, and add remote writers in `internal/shim/ssh.go`.

```go
type NotifyDeployState struct {
	Enabled        bool `json:"enabled"`
	HookInstalled  bool `json:"hook_installed"`
	CodexInjected  bool `json:"codex_injected"`
	HealthVerified bool `json:"health_verified"`
}

type DeployState struct {
	BinaryHash    string            `json:"binary_hash"`
	BinaryVersion string            `json:"binary_version"`
	ShimInstalled bool              `json:"shim_installed"`
	ShimTarget    string            `json:"shim_target"`
	PathFixed     bool              `json:"path_fixed"`
	Notify        *NotifyDeployState `json:"notify,omitempty"`
	Codex         *CodexDeployState `json:"codex,omitempty"`
}
```

- [ ] **Step 4: Add connect steps for nonce, hook install, Codex injection, and health probe**

Update `cmd/cc-clip/main.go` so the regular `connect` flow:

```go
notifyNonce, err := shim.GenerateNotificationNonce()
if err != nil {
	log.Fatalf("failed to generate notification nonce: %v", err)
}
srv.RegisterNotificationNonce(notifyNonce)
if err := shim.WriteRemoteNotificationNonce(session, notifyNonce); err != nil {
	log.Fatalf("failed to write notification nonce: %v", err)
}
if err := installRemoteHookScript(session, port); err != nil {
	log.Fatalf("failed to install remote hook script: %v", err)
}
if remoteHasCodex(session) {
	if err := ensureRemoteCodexNotifyConfig(session); err != nil {
		log.Printf("warning: codex config injection failed: %v", err)
	}
}
if err := runNotificationHealthProbe(session, remoteBin); err != nil {
	log.Printf("warning: notification health probe failed: %v", err)
}
```

- [ ] **Step 5: Re-run connect and deploy tests**

Run: `go test ./internal/shim ./cmd/cc-clip -count=1`

Expected: PASS.

- [ ] **Step 6: Run the project verification suite**

Run:

```bash
make test
make vet
```

Expected: both commands pass.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/shim/deploy.go internal/shim/ssh.go cmd/cc-clip/main.go cmd/cc-clip/main_test.go
git commit -m "feat: automate notification bridge setup"
```

## Final Verification Checklist

- [ ] Run: `go test ./internal/daemon -count=1`
- [ ] Run: `go test ./internal/shim -count=1`
- [ ] Run: `go test ./cmd/cc-clip -count=1`
- [ ] Run: `make test`
- [ ] Run: `make vet`
- [ ] Manual check: start `cc-clip serve`, POST a generic notification to `/notify`, confirm local popup
- [ ] Manual check: run `cc-clip connect <host>`, install Claude hook config, trigger remote hook, confirm local popup
- [ ] Manual check: on a remote with `~/.codex/`, verify `connect` injects Codex notify even without `--codex`
