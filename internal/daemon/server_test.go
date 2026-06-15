package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

type mockClipboard struct {
	clipType  ClipboardInfo
	imageData []byte
	text      string
	typeErr   error
	imageErr  error
	textErr   error
}

func (m *mockClipboard) Type() (ClipboardInfo, error) {
	return m.clipType, m.typeErr
}

func (m *mockClipboard) ImageBytes() ([]byte, error) {
	return m.imageData, m.imageErr
}

func (m *mockClipboard) Text() (string, error) {
	return m.text, m.textErr
}

func newTestServer(clip ClipboardReader) (*Server, string) {
	tm := token.NewManager(1 * time.Hour)
	s, _ := tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	return srv, s.Token
}

func withTokenDirOverride(t *testing.T) string {
	t.Helper()
	old := token.TokenDirOverride
	dir := t.TempDir()
	token.TokenDirOverride = dir
	t.Cleanup(func() {
		token.TokenDirOverride = old
	})
	return dir
}

type staticAddrListener struct {
	addr net.Addr
}

func (l staticAddrListener) Accept() (net.Conn, error) {
	return nil, errors.New("accept should not be reached")
}

func (l staticAddrListener) Close() error {
	return nil
}

func (l staticAddrListener) Addr() net.Addr {
	return l.addr
}

func TestServeRejectsNonLoopbackListener(t *testing.T) {
	srv, _ := newTestServer(&mockClipboard{})
	err := srv.Serve(staticAddrListener{
		addr: &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 18339},
	})
	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("expected non-loopback listener rejection, got %v", err)
	}
}

func TestHTTPServerHasDefensiveTimeouts(t *testing.T) {
	srv, _ := newTestServer(&mockClipboard{})
	httpSrv := srv.httpServer()

	if httpSrv.Handler != srv.mux {
		t.Fatal("http server should use server mux")
	}
	if httpSrv.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout must be configured")
	}
	if httpSrv.ReadTimeout <= 0 {
		t.Fatal("ReadTimeout must be configured")
	}
	if httpSrv.WriteTimeout <= 0 {
		t.Fatal("WriteTimeout must be configured")
	}
	if httpSrv.IdleTimeout <= 0 {
		t.Fatal("IdleTimeout must be configured")
	}
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
	if body["service"] != "cc-clip" {
		t.Fatalf("expected service cc-clip, got %s", body["service"])
	}
}

func TestRegisterNotificationNonceCapsRegistry(t *testing.T) {
	srv, _ := newTestServer(&mockClipboard{})

	for i := 0; i < 1100; i++ {
		nonce := fmt.Sprintf("nonce-%04d", i)
		host := fmt.Sprintf("host-%04d", i)
		if err := srv.RegisterNotificationNonceForHost(nonce, host); err != nil {
			t.Fatalf("register nonce %d: %v", i, err)
		}
	}

	if got := len(srv.notifyNonces); got > 1024 {
		t.Fatalf("expected nonce registry to cap at 1024 entries, got %d", got)
	}
	if !srv.validNotificationNonce("nonce-1099") {
		t.Fatal("most recently registered nonce should remain valid after eviction")
	}
}

func TestHandleRegisterNonceRevokesPreviousNonceForSameHost(t *testing.T) {
	srv, tok := newTestServer(&mockClipboard{})

	post := func(nonce, host string) int {
		body := fmt.Sprintf(`{"nonce":%q,"host":%q}`, nonce, host)
		req := httptest.NewRequest("POST", "/register-nonce", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("User-Agent", "cc-clip/connect")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		return w.Code
	}

	if code := post("nonce-old", "host-venus"); code != http.StatusNoContent {
		t.Fatalf("first register: expected 204, got %d", code)
	}
	if !srv.validNotificationNonce("nonce-old") {
		t.Fatal("first registration should make nonce-old valid")
	}

	if code := post("nonce-new", "host-venus"); code != http.StatusNoContent {
		t.Fatalf("second register: expected 204, got %d", code)
	}
	if !srv.validNotificationNonce("nonce-new") {
		t.Fatal("second registration should make nonce-new valid")
	}
	if srv.validNotificationNonce("nonce-old") {
		t.Fatal("second registration for the same host must revoke nonce-old; old nonce still valid")
	}
}

func TestRegisterNotificationNonceSameHostKeepsOrderBounded(t *testing.T) {
	srv, _ := newTestServer(&mockClipboard{})

	// Re-register N nonces for the SAME host. Each call must revoke the
	// previous nonce in BOTH the map AND the order slice; otherwise the
	// slice grows unbounded and the cap is defeated.
	for i := 0; i < 200; i++ {
		nonce := fmt.Sprintf("rotate-%04d", i)
		if err := srv.RegisterNotificationNonceForHost(nonce, "host-shared"); err != nil {
			t.Fatalf("register nonce %d: %v", i, err)
		}
	}

	if got := len(srv.notifyNonces); got != 1 {
		t.Fatalf("expected single live nonce after same-host re-registration, got %d", got)
	}
	if got := len(srv.notifyNoncesOrder); got != 1 {
		t.Fatalf("notifyNoncesOrder must stay in sync with the map; got %d entries", got)
	}
}

func TestCleanupExpiredNoncesShrinksOrderSlice(t *testing.T) {
	srv, _ := newTestServer(&mockClipboard{})

	// Register 50 nonces with unique hosts so revocation does not interfere.
	for i := 0; i < 50; i++ {
		nonce := fmt.Sprintf("nonce-%04d", i)
		host := fmt.Sprintf("host-%04d", i)
		if err := srv.RegisterNotificationNonceForHost(nonce, host); err != nil {
			t.Fatalf("register nonce %d: %v", i, err)
		}
	}

	// Manually expire every entry by rewriting ExpiresAt into the past.
	srv.noncesMu.Lock()
	for k, v := range srv.notifyNonces {
		v.ExpiresAt = time.Now().Add(-time.Hour)
		srv.notifyNonces[k] = v
	}
	srv.noncesMu.Unlock()

	srv.CleanupExpiredNonces()

	if got := len(srv.notifyNonces); got != 0 {
		t.Fatalf("expected map empty after cleanup, got %d", got)
	}
	if got := len(srv.notifyNoncesOrder); got != 0 {
		t.Fatalf("notifyNoncesOrder must be cleared by CleanupExpiredNonces; got %d entries", got)
	}
}

func TestNotificationNoncesPersistAndReload(t *testing.T) {
	withTokenDirOverride(t)
	srv, _ := newTestServer(&mockClipboard{})
	srv.EnableNoncePersistence()

	if err := srv.RegisterNotificationNonceForHost("nonce-persist", "venus"); err != nil {
		t.Fatalf("RegisterNotificationNonceForHost failed: %v", err)
	}

	path, err := nonceStorePath()
	if err != nil {
		t.Fatalf("nonceStorePath failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected nonce store to exist: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		got := info.Mode().Perm()
		t.Fatalf("nonce store mode = %o, want 0600", got)
	}

	reloaded, _ := newTestServer(&mockClipboard{})
	reloaded.EnableNoncePersistence()
	loaded, err := reloaded.LoadPersistedNonces()
	if err != nil {
		t.Fatalf("LoadPersistedNonces failed: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("loaded %d nonces, want 1", loaded)
	}
	host, ok := reloaded.lookupValidNonce("nonce-persist")
	if !ok {
		t.Fatal("persisted nonce should authenticate after reload")
	}
	if host != "venus" {
		t.Fatalf("reloaded host = %q, want venus", host)
	}
}

func TestLoadPersistedNoncesSkipsExpiredEntries(t *testing.T) {
	withTokenDirOverride(t)
	path, err := nonceStorePath()
	if err != nil {
		t.Fatalf("nonceStorePath failed: %v", err)
	}
	now := time.Now()
	store := persistedNonceStore{
		Version: 1,
		Nonces: []persistedNonce{
			{Nonce: "expired", Host: "old", IssuedAt: now.Add(-8 * 24 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
			{Nonce: "live", Host: "new", IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
		},
	}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write store: %v", err)
	}

	srv, _ := newTestServer(&mockClipboard{})
	srv.EnableNoncePersistence()
	loaded, err := srv.LoadPersistedNonces()
	if err != nil {
		t.Fatalf("LoadPersistedNonces failed: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("loaded %d nonces, want 1", loaded)
	}
	if srv.validNotificationNonce("expired") {
		t.Fatal("expired nonce should not be restored")
	}
	if !srv.validNotificationNonce("live") {
		t.Fatal("live nonce should be restored")
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

func TestClipboardTypeRejectsMissingUserAgentWithAuth(t *testing.T) {
	clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardImage, Format: "png"}}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/type", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without User-Agent, got %d", w.Code)
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

func TestClipboardImageSourceTooLarge(t *testing.T) {
	clip := &mockClipboard{
		clipType: ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageErr: clipboardOutputLimitError{
			msg: "clipboard image exceeds 20MB limit",
		},
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

func TestClipboardTextReturnsData(t *testing.T) {
	clip := &mockClipboard{
		clipType: ClipboardInfo{Type: ClipboardText},
		text:     "hello from local clipboard\n",
	}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/text", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	if got := w.Body.String(); got != clip.text {
		t.Fatalf("body = %q, want %q", got, clip.text)
	}
}

func TestClipboardTextNoContentWhenImage(t *testing.T) {
	clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardImage, Format: "png"}}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/text", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestClipboardTextTooLarge(t *testing.T) {
	clip := &mockClipboard{
		clipType: ClipboardInfo{Type: ClipboardText},
		text:     strings.Repeat("x", maxTextSize()+1),
	}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/text", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}

func TestClipboardTextSourceTooLarge(t *testing.T) {
	clip := &mockClipboard{
		clipType: ClipboardInfo{Type: ClipboardText},
		textErr: clipboardOutputLimitError{
			msg: "clipboard text exceeds 1MB limit",
		},
	}
	srv, tok := newTestServer(clip)

	req := httptest.NewRequest("GET", "/clipboard/text", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}

func TestClipboardSizeLimitEnvOverrides(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"", defaultMaxTextMB},
		{"5", 5},
		{"0", defaultMaxTextMB},
		{"-3", defaultMaxTextMB},
		{"abc", defaultMaxTextMB},
	}
	for _, tc := range cases {
		t.Setenv("CC_CLIP_MAX_TEXT_MB", tc.env)
		if got := maxTextMB(); got != tc.want {
			t.Errorf("CC_CLIP_MAX_TEXT_MB=%q: maxTextMB() = %d, want %d", tc.env, got, tc.want)
		}
	}
	t.Setenv("CC_CLIP_MAX_TEXT_MB", "2")
	if got := maxTextSize(); got != 2*1024*1024 {
		t.Errorf("maxTextSize() = %d, want %d", got, 2*1024*1024)
	}

	if got := maxImageMB(); got != defaultMaxImageMB {
		t.Errorf("maxImageMB() = %d, want default %d", got, defaultMaxImageMB)
	}
	t.Setenv("CC_CLIP_MAX_IMAGE_MB", "30")
	if got := maxImageSize(); got != 30*1024*1024 {
		t.Errorf("maxImageSize() = %d, want %d", got, 30*1024*1024)
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

func TestParseNotifyRequestRejectsOversizedBody(t *testing.T) {
	srv, _ := newTestServer(&mockClipboard{})
	body := strings.NewReader(strings.Repeat("x", maxNotifyBody+1))
	req := httptest.NewRequest("POST", "/notify", body)

	_, err := srv.parseNotifyRequest(req)
	if err == nil {
		t.Fatal("expected oversized notification body to fail")
	}
	if !strings.Contains(err.Error(), "64KB limit") {
		t.Fatalf("error = %q, want 64KB limit", err.Error())
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

func TestNotifyOverridesBodyHostWithNonceBoundHost(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	// Nonce is bound to host A. Identity must come from this auth context.
	if err := srv.RegisterNotificationNonceForHost("nonce-hostA", "host-A"); err != nil {
		t.Fatalf("register nonce: %v", err)
	}

	// Body claims host B (spoof attempt).
	body := strings.NewReader(`{"title":"hi","body":"there","host":"host-B"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-hostA")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}

	select {
	case env := <-srv.notifyCh:
		if env.Host != "host-A" {
			t.Fatalf("expected host to be authoritative nonce host %q, got %q", "host-A", env.Host)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected an envelope to be enqueued")
	}
}

func TestNotifyOverridesClaudeHookHostWithNonceBoundHost(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	if err := srv.RegisterNotificationNonceForHost("nonce-claudeA", "host-A"); err != nil {
		t.Fatalf("register nonce: %v", err)
	}

	// Claude hook body injects a spoofed _cc_clip_host. urgency 0 (stop) routes to notifyCh.
	body := strings.NewReader(`{"hook_event_name":"Stop","stop_hook_reason":"stop_at_end_of_turn","last_assistant_message":"done","_cc_clip_host":"host-B"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-claudeA")
	req.Header.Set("Content-Type", "application/x-claude-hook")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}

	select {
	case env := <-srv.notifyCh:
		if env.Host != "host-A" {
			t.Fatalf("expected host to be authoritative nonce host %q, got %q", "host-A", env.Host)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected an envelope to be enqueued")
	}
}

func TestNotifyKeepsBodyHostWhenNonceUnbound(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	// Nonce registered with no host binding.
	if err := srv.RegisterNotificationNonce("nonce-unbound"); err != nil {
		t.Fatalf("register nonce: %v", err)
	}

	body := strings.NewReader(`{"title":"hi","body":"there","host":"host-from-body"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-unbound")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}

	select {
	case env := <-srv.notifyCh:
		if env.Host != "host-from-body" {
			t.Fatalf("expected body host preserved for unbound nonce, got %q", env.Host)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected an envelope to be enqueued")
	}
}

func TestNotifyParsesTrustedAndSoundFromGenericPayload(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	if err := srv.RegisterNotificationNonce("nonce-trusted"); err != nil {
		t.Fatalf("register nonce: %v", err)
	}

	body := strings.NewReader(`{"title":"hi","body":"there","trusted":true,"sound":"Ping"}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-trusted")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", w.Code, w.Body.String())
	}

	select {
	case env := <-srv.notifyCh:
		if env.GenericMessage == nil {
			t.Fatal("expected generic message")
		}
		if !env.GenericMessage.Verified {
			t.Fatal("trusted=true should mark message verified")
		}
		if env.GenericMessage.Sound != "Ping" {
			t.Fatalf("sound = %q, want Ping", env.GenericMessage.Sound)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected an envelope to be enqueued")
	}
}

func TestNotifyRejectsGenericPayloadWithEmptyTitleAndBody(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)
	srv.RegisterNotificationNonce("nonce-blank")

	body := strings.NewReader(`{"urgency":1}`)
	req := httptest.NewRequest("POST", "/notify", body)
	req.Header.Set("Authorization", "Bearer nonce-blank")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty title and body, got %d", w.Code)
	}
}

func TestParseGenericJSONRequiresTitleOrBody(t *testing.T) {
	clip := &mockClipboard{}
	tm := token.NewManager(time.Hour)
	_, _ = tm.Generate()
	store := session.NewStore(12 * time.Hour)
	srv := NewServer("127.0.0.1:0", clip, tm, store)

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"both empty", `{"urgency":1}`, true},
		{"title only", `{"title":"hi"}`, false},
		{"body only", `{"body":"there"}`, false},
		{"both present", `{"title":"hi","body":"there"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.parseGenericJSON([]byte(tt.body))
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for body %q, got nil", tt.body)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for body %q: %v", tt.body, err)
			}
		})
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
	req.Header.Set("User-Agent", "cc-clip/0.1")
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
	req.Header.Set("User-Agent", "cc-clip/0.1")
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
	req.Header.Set("User-Agent", "cc-clip/0.1")
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
