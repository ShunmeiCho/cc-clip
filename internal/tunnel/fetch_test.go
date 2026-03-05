package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

// newIPv4TestServer creates an httptest server bound to 127.0.0.1 (IPv4 only).
// Returns nil and skips the test if binding fails (restricted sandbox/CI).
func newIPv4TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind 127.0.0.1: %v (restricted environment?)", err)
		return nil
	}
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener.Close()
	ts.Listener = l
	ts.Start()
	return ts
}

func TestFetchImageRoundTrip(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("GET /clipboard/type", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(daemon.ClipboardInfo{Type: daemon.ClipboardImage, Format: "png"})
	})
	mux.HandleFunc("GET /clipboard/image", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(fakeImage)
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	client := NewClient(ts.URL, testToken, 5*time.Second)

	info, err := client.ClipboardType()
	if err != nil {
		t.Fatalf("ClipboardType failed: %v", err)
	}
	if info.Type != daemon.ClipboardImage {
		t.Fatalf("expected image, got %s", info.Type)
	}

	outDir := t.TempDir()
	path, err := client.FetchImage(outDir)
	if err != nil {
		t.Fatalf("FetchImage failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read saved image: %v", err)
	}
	if len(data) != len(fakeImage) {
		t.Fatalf("expected %d bytes, got %d", len(fakeImage), len(data))
	}

	t.Logf("Image saved to: %s (%d bytes)", path, len(data))
}

func TestFetchImageUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /clipboard/image", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	client := NewClient(ts.URL, "wrong-token", 5*time.Second)

	_, err := client.FetchImage(t.TempDir())
	if err == nil {
		t.Fatal("expected error for unauthorized request")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got: %v", err)
	}
}

func TestClipboardTypeUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /clipboard/type", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	client := NewClient(ts.URL, "wrong-token", 5*time.Second)

	_, err := client.ClipboardType()
	if err == nil {
		t.Fatal("expected error for unauthorized request")
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got: %v", err)
	}
}

func TestProbeSuccess(t *testing.T) {
	ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()

	addr := ts.Listener.Addr().String()
	if err := Probe(addr, 500*time.Millisecond); err != nil {
		t.Fatalf("probe should succeed: %v", err)
	}
}

func TestProbeFailure(t *testing.T) {
	err := Probe(fmt.Sprintf("127.0.0.1:%d", 59999), 100*time.Millisecond)
	if err == nil {
		t.Fatal("probe should fail for closed port")
	}
}
