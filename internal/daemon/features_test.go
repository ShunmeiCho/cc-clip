package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseFeatures(t *testing.T) {
	cases := []struct {
		in   string
		want Features
	}{
		{"", AllFeatures},
		{"all", AllFeatures},
		{"ALL", AllFeatures},
		{"none", Features{}},
		{"image", Features{Image: true}},
		{" image , notify ", Features{Image: true, Notify: true}},
		{"text", Features{TextRead: true, TextWrite: true}},
		{"text-read", Features{TextRead: true}},
		{"image,text-write", Features{Image: true, TextWrite: true}},
		{"image,text,notify", AllFeatures},
	}
	for _, c := range cases {
		got, err := ParseFeatures(c.in)
		if err != nil {
			t.Fatalf("ParseFeatures(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseFeatures(%q) = %+v, want %+v", c.in, got, c.want)
		}
		if again, _ := ParseFeatures(got.String()); again != got {
			t.Fatalf("String() of %+v does not round-trip: %q", got, got.String())
		}
	}
	for _, bad := range []string{"images", "image,clipboard", "text=off"} {
		if _, err := ParseFeatures(bad); err == nil {
			t.Fatalf("ParseFeatures(%q) accepted an unknown feature", bad)
		}
	}
}

func featureRequest(t *testing.T, srv *Server, tok, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

func clipboardTypeOf(t *testing.T, srv *Server, tok string) ClipboardType {
	t.Helper()
	var info ClipboardInfo
	w := featureRequest(t, srv, tok, "GET", "/clipboard/type", "")
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode /clipboard/type: %v", err)
	}
	return info.Type
}

func expectDisabled(t *testing.T, w *httptest.ResponseRecorder, feature string) {
	t.Helper()
	if w.Code != http.StatusForbidden {
		t.Fatalf("%s disabled: expected 403, got %d", feature, w.Code)
	}
	if got := w.Header().Get(DisabledFeatureHeader); got != feature {
		t.Fatalf("%s disabled: %s = %q", feature, DisabledFeatureHeader, got)
	}
}

// TestFeaturesGateText pins each text direction against the text endpoints: a
// disabled direction answers 403 without reading or writing the local
// clipboard, and /clipboard/type does not reveal that text exists.
func TestFeaturesGateText(t *testing.T) {
	const secret = "hunter2"
	for _, f := range []Features{{}, {TextRead: true}, {TextWrite: true}, AllFeatures} {
		t.Run(f.String(), func(t *testing.T) {
			clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardText}, text: secret}
			srv, tok := newTestServer(clip)
			writer := &mockTextWriter{}
			srv.SetTextWriter(writer)
			srv.SetFeatures(f)

			read := featureRequest(t, srv, tok, "GET", "/clipboard/text", "")
			if f.TextRead {
				if read.Code != http.StatusOK || read.Body.String() != secret {
					t.Fatalf("read enabled: got %d %q", read.Code, read.Body.String())
				}
			} else {
				expectDisabled(t, read, "text-read")
				if strings.Contains(read.Body.String(), secret) {
					t.Fatal("read disabled: response leaked clipboard text")
				}
			}

			wantType := ClipboardEmpty
			if f.TextRead {
				wantType = ClipboardText
			}
			if got := clipboardTypeOf(t, srv, tok); got != wantType {
				t.Fatalf("/clipboard/type = %q, want %q", got, wantType)
			}

			write := featureRequest(t, srv, tok, "POST", "/clipboard/text", "payload")
			if f.TextWrite {
				if write.Code != http.StatusNoContent || len(writer.written) != 1 {
					t.Fatalf("write enabled: got %d, writes %q", write.Code, writer.written)
				}
			} else {
				expectDisabled(t, write, "text-write")
				if len(writer.written) != 0 {
					t.Fatalf("write disabled: writer was called with %q", writer.written)
				}
			}
		})
	}
}

// TestFeaturesGateImage: with image off the image is neither served nor
// reported, so the remote shim falls through to the real xclip.
func TestFeaturesGateImage(t *testing.T) {
	for _, f := range []Features{{Image: true}, {TextRead: true, TextWrite: true, Notify: true}} {
		t.Run(f.String(), func(t *testing.T) {
			clip := &mockClipboard{
				clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
				imageData: []byte("\x89PNG fake"),
			}
			srv, tok := newTestServer(clip)
			srv.SetFeatures(f)

			img := featureRequest(t, srv, tok, "GET", "/clipboard/image", "")
			wantType := ClipboardImage
			if f.Image {
				if img.Code != http.StatusOK || img.Body.String() != string(clip.imageData) {
					t.Fatalf("image enabled: got %d", img.Code)
				}
			} else {
				expectDisabled(t, img, "image")
				if strings.Contains(img.Body.String(), "PNG") {
					t.Fatal("image disabled: response leaked image data")
				}
				wantType = ClipboardEmpty
			}
			if got := clipboardTypeOf(t, srv, tok); got != wantType {
				t.Fatalf("/clipboard/type = %q, want %q", got, wantType)
			}
		})
	}
}

// TestFeaturesGateNotify: with notify off a nonce can be neither registered
// nor used, so a remote process cannot put text on the local screen.
func TestFeaturesGateNotify(t *testing.T) {
	srv, tok := newTestServer(&mockClipboard{})
	srv.SetFeatures(Features{Image: true})

	reg := featureRequest(t, srv, tok, "POST", "/register-nonce", `{"nonce":"0123456789abcdef0123456789abcdef"}`)
	expectDisabled(t, reg, "notify")
	note := featureRequest(t, srv, tok, "POST", "/notify", `{"title":"x","body":"y"}`)
	expectDisabled(t, note, "notify")
	if n := len(srv.notifyCh) + len(srv.criticalCh); n != 0 {
		t.Fatalf("notify off: %d notifications queued, want 0", n)
	}
}

// TestFeaturesNotifyOffSkipsImageTransferNotification: image paste keeps
// working, it just stops announcing itself.
func TestFeaturesNotifyOffSkipsImageTransferNotification(t *testing.T) {
	for _, f := range []Features{AllFeatures, {Image: true}} {
		clip := &mockClipboard{
			clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
			imageData: []byte("\x89PNG fake"),
		}
		srv, tok := newTestServer(clip)
		srv.SetFeatures(f)

		req := httptest.NewRequest("GET", "/clipboard/image", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("User-Agent", "cc-clip/0.1")
		req.Header.Set("X-CC-Clip-Session", "test-session")
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: image fetch got %d", f, w.Code)
		}
		queued := len(srv.notifyCh) + len(srv.criticalCh)
		if f.Notify && queued == 0 {
			t.Fatal("notify on: image transfer should queue a notification (test precondition)")
		}
		if !f.Notify && queued != 0 {
			t.Fatalf("notify off: image transfer queued %d notifications, want 0", queued)
		}
	}
}
