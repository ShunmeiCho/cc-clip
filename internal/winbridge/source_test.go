package winbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPSourceReadsTokenEachRequestAndBoundsResponses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte("first"), 0600)
	expected := "first"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expected || r.Header.Get("User-Agent") != "cc-clip/win-bridge" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/clipboard/type":
			w.Write([]byte(`{"type":"image","format":"png","revision":42}`))
		case "/clipboard/image":
			w.Write([]byte{0, 255, 10, 13})
		}
	}))
	defer srv.Close()
	source := NewHTTPSource(1, file)
	source.BaseURL = srv.URL
	info, err := source.Type(context.Background())
	if err != nil || info.Revision != 42 {
		t.Fatalf("invalid clipboard metadata: %+v %v", info, err)
	}
	expected = "second"
	os.WriteFile(file, []byte(expected), 0600)
	data, err := source.Image(context.Background())
	if err != nil || len(data) != 4 || data[1] != 255 {
		t.Fatalf("token reload/image failed: %v", err)
	}
	os.WriteFile(file, []byte("wrong"), 0600)
	if _, err := source.Image(context.Background()); err == nil {
		t.Fatal("bad token accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Type(ctx); err == nil {
		t.Fatal("canceled request succeeded")
	}
}

func TestHTTPSourceRejectsEmptyAndOversizedImages(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte("token"), 0600)
	for _, body := range []string{"", strings.Repeat("x", MaxEncodedBytes+1)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		source := NewHTTPSource(1, file)
		source.BaseURL = srv.URL
		if _, err := source.Image(context.Background()); err == nil {
			t.Fatal("unbounded/empty image accepted")
		}
		srv.Close()
	}
}
