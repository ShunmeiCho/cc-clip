package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTextMode(t *testing.T) {
	cases := []struct {
		in   string
		want TextMode
	}{
		{"", TextModeBoth},
		{"both", TextModeBoth},
		{"BOTH", TextModeBoth},
		{"off", TextMode{}},
		{"none", TextMode{}},
		{" read ", TextMode{Read: true}},
		{"write", TextMode{Write: true}},
	}
	for _, c := range cases {
		got, err := ParseTextMode(c.in)
		if err != nil {
			t.Fatalf("ParseTextMode(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseTextMode(%q) = %+v, want %+v", c.in, got, c.want)
		}
		if again, _ := ParseTextMode(got.String()); again != got {
			t.Fatalf("String() of %+v does not round-trip: %q", got, got.String())
		}
	}
	if _, err := ParseTextMode("yes"); err == nil {
		t.Fatal("ParseTextMode accepted an unknown mode")
	}
}

func textModeRequest(t *testing.T, srv *Server, tok, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

// TestTextModeGatesEndpoints pins each mode against all three text-facing
// endpoints: a disabled direction must answer 403 without reading or writing
// the local clipboard, and /clipboard/type must not reveal that text exists.
func TestTextModeGatesEndpoints(t *testing.T) {
	const secret = "hunter2"
	for _, mode := range []TextMode{{}, {Read: true}, {Write: true}, TextModeBoth} {
		t.Run(mode.String(), func(t *testing.T) {
			clip := &mockClipboard{clipType: ClipboardInfo{Type: ClipboardText}, text: secret}
			srv, tok := newTestServer(clip)
			writer := &mockTextWriter{}
			srv.SetTextWriter(writer)
			srv.SetTextMode(mode)

			read := textModeRequest(t, srv, tok, "GET", "/clipboard/text", "")
			if mode.Read {
				if read.Code != http.StatusOK || read.Body.String() != secret {
					t.Fatalf("read enabled: got %d %q", read.Code, read.Body.String())
				}
			} else {
				if read.Code != http.StatusForbidden {
					t.Fatalf("read disabled: expected 403, got %d", read.Code)
				}
				if strings.Contains(read.Body.String(), secret) {
					t.Fatal("read disabled: response leaked clipboard text")
				}
			}

			typ := textModeRequest(t, srv, tok, "GET", "/clipboard/type", "")
			var info ClipboardInfo
			if err := json.Unmarshal(typ.Body.Bytes(), &info); err != nil {
				t.Fatalf("decode /clipboard/type: %v", err)
			}
			wantType := ClipboardEmpty
			if mode.Read {
				wantType = ClipboardText
			}
			if info.Type != wantType {
				t.Fatalf("/clipboard/type = %q, want %q", info.Type, wantType)
			}

			write := textModeRequest(t, srv, tok, "POST", "/clipboard/text", "payload")
			if mode.Write {
				if write.Code != http.StatusNoContent || len(writer.written) != 1 {
					t.Fatalf("write enabled: got %d, writes %q", write.Code, writer.written)
				}
			} else {
				if write.Code != http.StatusForbidden {
					t.Fatalf("write disabled: expected 403, got %d", write.Code)
				}
				if len(writer.written) != 0 {
					t.Fatalf("write disabled: writer was called with %q", writer.written)
				}
			}
		})
	}
}

// TestTextModeLeavesImagesAlone guards the point of the feature: turning text
// off must never break image paste.
func TestTextModeLeavesImagesAlone(t *testing.T) {
	clip := &mockClipboard{
		clipType:  ClipboardInfo{Type: ClipboardImage, Format: "png"},
		imageData: []byte("\x89PNG fake"),
	}
	srv, tok := newTestServer(clip)
	srv.SetTextMode(TextMode{})

	typ := textModeRequest(t, srv, tok, "GET", "/clipboard/type", "")
	if !strings.Contains(typ.Body.String(), `"image"`) {
		t.Fatalf("/clipboard/type with text off = %s, want image", typ.Body.String())
	}
	img := textModeRequest(t, srv, tok, "GET", "/clipboard/image", "")
	if img.Code != http.StatusOK || img.Body.String() != string(clip.imageData) {
		t.Fatalf("/clipboard/image with text off: got %d", img.Code)
	}
}
