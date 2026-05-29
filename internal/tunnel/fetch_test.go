package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

type failingReader struct {
	err error
}

func (r failingReader) Read([]byte) (int, error) {
	return 0, r.err
}

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

func TestFetchImageWritesPrivateFileMode(t *testing.T) {
	fakeImage := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("GET /clipboard/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(fakeImage)
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	client := NewClient(ts.URL, testToken, 5*time.Second)
	outDir := t.TempDir()

	path, err := client.FetchImage(outDir)
	if err != nil {
		t.Fatalf("FetchImage failed: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat saved image: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected file mode 0600, got %o", perm)
	}
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

func TestFetchImageFailsWhenRandomSuffixGenerationFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /clipboard/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("png-data"))
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	oldReader := randReader
	randReader = failingReader{err: errors.New("entropy unavailable")}
	defer func() {
		randReader = oldReader
	}()

	client := NewClient(ts.URL, "test-token", 5*time.Second)
	outDir := t.TempDir()
	_, err := client.FetchImage(outDir)
	if err == nil {
		t.Fatal("expected random suffix generation error")
	}
	if !strings.Contains(err.Error(), "filename suffix") {
		t.Fatalf("expected filename suffix error, got %v", err)
	}

	entries, readErr := os.ReadDir(outDir)
	if readErr != nil {
		t.Fatalf("failed to read output dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no partial files, found %d", len(entries))
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

func TestProbeHealthSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"cc-clip"}`))
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	addr := ts.Listener.Addr().String()
	if err := ProbeHealth(addr, 500*time.Millisecond); err != nil {
		t.Fatalf("ProbeHealth should succeed: %v", err)
	}
}

func TestProbeHealthRejectsNonCcClipHealthBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"not-cc-clip"}`))
	})

	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	err := ProbeHealth(ts.Listener.Addr().String(), 500*time.Millisecond)
	if err == nil {
		t.Fatal("ProbeHealth should reject a generic HTTP 200 health endpoint")
	}
	if !errors.Is(err, ErrDaemonNotAnswering) {
		t.Fatalf("expected ErrDaemonNotAnswering, got: %v", err)
	}
}

func TestProbeHealthFailsWhenTCPUnreachable(t *testing.T) {
	err := ProbeHealth(fmt.Sprintf("127.0.0.1:%d", 59998), 100*time.Millisecond)
	if err == nil {
		t.Fatal("ProbeHealth should fail for closed port")
	}
	if errors.Is(err, ErrDaemonNotAnswering) {
		t.Fatalf("closed port should not report ErrDaemonNotAnswering, got: %v", err)
	}
}

func TestProbeHealthFailsWhenTCPUpButDaemonDead(t *testing.T) {
	// A listener that accepts the TCP handshake but never speaks HTTP — this
	// mimics an SSH RemoteForward listener whose local daemon is down.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind 127.0.0.1: %v (restricted environment?)", err)
	}
	defer l.Close()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			// Hold the connection open without responding so the HTTP
			// client times out waiting for headers.
			go func(c net.Conn) {
				time.Sleep(2 * time.Second)
				c.Close()
			}(conn)
		}
	}()

	err = ProbeHealth(l.Addr().String(), 200*time.Millisecond)
	if err == nil {
		t.Fatal("ProbeHealth should fail when daemon does not answer HTTP")
	}
	if !errors.Is(err, ErrDaemonNotAnswering) {
		t.Fatalf("expected ErrDaemonNotAnswering, got: %v", err)
	}
}
