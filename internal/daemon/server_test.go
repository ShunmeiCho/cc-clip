package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	srv := NewServer("127.0.0.1:0", clip, tm)
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
