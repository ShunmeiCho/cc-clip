package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

type mockClipboard struct {
	clipType  ClipboardInfo
	imageData []byte
	typeErr   error
	imageErr  error
}

func (m *mockClipboard) Type() (ClipboardInfo, error) {
	return m.clipType, m.typeErr
}

func (m *mockClipboard) ImageBytes() ([]byte, error) {
	return m.imageData, m.imageErr
}

func newTestServer(clip ClipboardReader) (*Server, string) {
	tm := token.NewManager(1 * time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	return srv, s.Token
}

func TestHealthEndpoint(t *testing.T) {
	clip := &mockClipboard{}
	srv, _ := newTestServer(clip)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", body["status"])
	}
}

func TestClipboardTypeRequiresAuth(t *testing.T) {
	clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardImage, Format: "png"}}
	srv, _ := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/type", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}
}

func TestClipboardTypeWithAuth(t *testing.T) {
	clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardImage, Format: "png"}}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/type", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var info ClipboardInfo
	json.NewDecoder(w.Body).Decode(&info)
	if info.Type != ClipboardImage {
		t.Fatalf("expected image type, got %s", info.Type)
	}
	if info.Format != "png" {
		t.Fatalf("expected png format, got %s", info.Format)
	}
}

func TestClipboardImageReturnsData(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: fakeImage,
	}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("expected image/png content type, got %s", w.Header().Get("Content-Type"))
	}

	body, _ := io.ReadAll(w.Body)
	if len(body) != len(fakeImage) {
		t.Fatalf("expected %d bytes, got %d", len(fakeImage), len(body))
	}
}

func TestClipboardImageNoContent(t *testing.T) {
	clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardText}}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestClipboardImageEmptyBytesNoContent(t *testing.T) {
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: []byte{},
	}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for empty image bytes, got %d", w.Code)
	}
}

func TestClipboardImageTooLarge(t *testing.T) {
	bigImage := make([]byte, 21*1024*1024) // 21MB
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: bigImage,
	}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/image", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}

func TestWrongTokenRejected(t *testing.T) {
	clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardImage, Format: "png"}}
	srv, _ := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/type", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- /notify endpoint tests ---

func TestNotifyEndpointAcceptsClaudeHookPayload(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
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
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestNotifyEndpointRejectsInvalidNonce(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	// No nonce registered

	body := strings.NewReader(`{"title":"test","body":"hello"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer bad-nonce")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad nonce, got %d", w.Code)
	}
}

func TestNotifyEndpointRejectsMissingAuth(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	body := strings.NewReader(`{"title":"test","body":"hello"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %d", w.Code)
	}
}

func TestNotifyEndpointAcceptsGenericJSON(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-abc")

	body := strings.NewReader(`{"title":"Build done","body":"All tests passed","urgency":1}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-abc")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestNotifyEndpointRejectsClipboardToken(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	// Do NOT register clipboard token as nonce

	body := strings.NewReader(`{"title":"test","body":"hello"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (clipboard token should not work for /notify), got %d", w.Code)
	}
}

func TestNotifyEndpointRejectsEmptyBody(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-xyz")

	req := httptest.NewRequest("POST", "/notify", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer nonce-xyz")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty body, got %d", w.Code)
	}
}

// --- /register-nonce endpoint tests ---

func TestRegisterNonceEndpointRegistersNonce(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	// Register a nonce via the endpoint
	body := strings.NewReader(`{"nonce":"test-nonce-abc123"}`)
	req := httptest.NewRequest("POST", "/register-nonce", body)
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify the nonce is now usable for /notify
	notifyBody := strings.NewReader(`{"title":"test","body":"hello"}`)
	notifyReq := httptest.NewRequest("POST", "/notify", notifyBody)
	notifyReq.Header.Set("Authorization", "Bearer test-nonce-abc123")
	notifyReq.Header.Set("Content-Type", "application/json")
	notifyW := httptest.NewRecorder()

	srv.mux.ServeHTTP(notifyW, notifyReq)

	if notifyW.Code != http.StatusNoContent {
		t.Fatalf("expected 204 after nonce registration, got %d", notifyW.Code)
	}
}

func TestRegisterNonceEndpointRequiresAuth(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	body := strings.NewReader(`{"nonce":"some-nonce"}`)
	req := httptest.NewRequest("POST", "/register-nonce", body)
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}
}

func TestRegisterNonceEndpointRejectsEmptyNonce(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	body := strings.NewReader(`{"nonce":""}`)
	req := httptest.NewRequest("POST", "/register-nonce", body)
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty nonce, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestRegisterNonceEndpointRejectsInvalidJSON(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	body := strings.NewReader(`{invalid json`)
	req := httptest.NewRequest("POST", "/register-nonce", body)
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestDedupSuppressesRepeatedNotifyAtRuntime(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-dedup")

	postNotify := func() int {
		body := strings.NewReader(`{"title":"Claude finished","body":"Done","urgency":0}`)
		req := httptest.NewRequest("POST", "/notify", body)
		req.Header.Set("Authorization", "Bearer nonce-dedup")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		return w.Code
	}

	// First request: accepted (204)
	if code := postNotify(); code != http.StatusNoContent {
		t.Fatalf("first notify: expected 204, got %d", code)
	}

	// Second identical request within dedup window: still 204 from handler
	// (dedup happens at enqueue, not at HTTP level) but the envelope
	// should NOT reach the channel.
	if code := postNotify(); code != http.StatusNoContent {
		t.Fatalf("second notify: expected 204, got %d", code)
	}

	// Only one envelope should have been enqueued
	select {
	case <-srv.notifyCh:
		// good, first one is there
	default:
		t.Fatal("expected first envelope in notifyCh")
	}
	select {
	case <-srv.notifyCh:
		t.Fatal("dedup should have suppressed the second envelope")
	default:
		// good, channel is empty
	}
}

func TestDedupDoesNotSuppressCriticalPermissionPrompt(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-crit-dedup")

	postCritical := func() int {
		body := strings.NewReader(`{"hook_event_name":"Notification","type":"permission_prompt","title":"Approve","body":"Edit main.go","_cc_clip_host":"venus"}`)
		req := httptest.NewRequest("POST", "/notify", body)
		req.Header.Set("Authorization", "Bearer nonce-crit-dedup")
		req.Header.Set("Content-Type", "application/x-claude-hook")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		return w.Code
	}

	// Send two identical permission_prompt notifications
	if code := postCritical(); code != http.StatusNoContent {
		t.Fatalf("first critical: expected 204, got %d", code)
	}
	if code := postCritical(); code != http.StatusNoContent {
		t.Fatalf("second critical: expected 204, got %d", code)
	}

	// Both should be in criticalCh (not deduped)
	count := 0
	for range 2 {
		select {
		case <-srv.criticalCh:
			count++
		case <-time.After(200 * time.Millisecond):
			// timeout
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 critical envelopes, got %d", count)
	}
}
