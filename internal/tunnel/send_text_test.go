package tunnel

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSendTextRoundTrip pins the client half of #128: the body reaches the
// daemon VERBATIM (soft-wrap corruption is the whole point of the feature) and
// carries the Bearer token + cc-clip User-Agent the daemon's auth requires.
func TestSendTextRoundTrip(t *testing.T) {
	const testToken = "test-token-123"
	payload := "line one\nline two with trailing\n"

	var gotBody, gotAuth, gotUA string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /clipboard/text", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotAuth, gotUA = string(b), r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusNoContent)
	})
	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	client := NewClient(ts.URL, testToken, 5*time.Second)
	if err := client.SendText(payload); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if gotBody != payload {
		t.Fatalf("daemon received %q, want exactly %q", gotBody, payload)
	}
	if gotAuth != "Bearer "+testToken {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.HasPrefix(gotUA, "cc-clip") {
		t.Fatalf("User-Agent = %q, want cc-clip prefix", gotUA)
	}
}

func TestSendTextErrorMapping(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantSub string
		wantTok bool
	}{
		{"unauthorized maps to ErrTokenInvalid", http.StatusUnauthorized, "", true},
		{"too large names the limit", http.StatusRequestEntityTooLarge, "size limit", false},
		// An old local daemon has GET /clipboard/text but no POST pattern, so
		// Go's ServeMux answers 405; a daemon built without a writer answers
		// 501. Both mean the same thing to the user: upgrade the LOCAL side.
		{"405 from an old daemon says upgrade local", http.StatusMethodNotAllowed, "local machine", false},
		{"501 without writer says upgrade local", http.StatusNotImplemented, "local machine", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /clipboard/text", func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", tc.status)
			})
			ts := newIPv4TestServer(t, mux)
			defer ts.Close()

			err := NewClient(ts.URL, "tok", 5*time.Second).SendText("x")
			if err == nil {
				t.Fatal("want error")
			}
			if tc.wantTok && err != ErrTokenInvalid {
				t.Fatalf("err = %v, want ErrTokenInvalid", err)
			}
			if tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err %q must mention %q", err, tc.wantSub)
			}
		})
	}
}

// TestFetchHealth pins the /health probe used by `cc-clip status` for the
// daemon-version mismatch check (#22 P2). Unknown fields must not break older
// daemons' responses (version absent -> empty string).
func TestFetchHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"service":"cc-clip","status":"ok","version":"0.9.3"}`))
	})
	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	h, err := FetchHealth(strings.TrimPrefix(ts.URL, "http://"), 2*time.Second)
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}
	if h.Service != "cc-clip" || h.Status != "ok" || h.Version != "0.9.3" {
		t.Fatalf("health = %+v", h)
	}
}

func TestFetchHealthOldDaemonWithoutVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"service":"cc-clip","status":"ok"}`))
	})
	ts := newIPv4TestServer(t, mux)
	defer ts.Close()

	h, err := FetchHealth(strings.TrimPrefix(ts.URL, "http://"), 2*time.Second)
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}
	if h.Version != "" {
		t.Fatalf("old daemon must yield empty version, got %q", h.Version)
	}
}
