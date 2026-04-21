package shim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestXclipShimSubstitutesPortAndRealBinary(t *testing.T) {
	got := XclipShim(18339, "/usr/bin/xclip")
	if !strings.Contains(got, `CC_CLIP_PORT="${CC_CLIP_PORT:-18339}"`) {
		t.Fatalf("port substitution missing: %q", got)
	}
	if !strings.Contains(got, `REAL_XCLIP="/usr/bin/xclip"`) {
		t.Fatalf("real xclip path substitution missing: %q", got)
	}
}

func TestWlPasteShimSubstitutesPortAndRealBinary(t *testing.T) {
	got := WlPasteShim(18339, "/usr/bin/wl-paste")
	if !strings.Contains(got, `CC_CLIP_PORT="${CC_CLIP_PORT:-18339}"`) {
		t.Fatalf("port substitution missing: %q", got)
	}
	if !strings.Contains(got, `REAL_WL_PASTE="/usr/bin/wl-paste"`) {
		t.Fatalf("real wl-paste path substitution missing: %q", got)
	}
}

// imagePayload is a distinctive byte sequence served by the mock daemon for
// /clipboard/image. Tests assert that when a shim pattern actually intercepts
// an invocation, this exact payload reaches stdout — which is only possible if
// the shim went through the HTTP path rather than falling through to the real
// binary.
var imagePayload = []byte("CC-CLIP-INTERCEPTED-PAYLOAD")

const expectedTypeToken = "image/png"

// startMockDaemon serves the two endpoints the shim consumes when
// intercepting: /clipboard/type (JSON `{"type":"image","format":"png"}`) and
// /clipboard/image (raw bytes). Returns (port, tokenFilePath).
func startMockDaemon(t *testing.T, tmpDir string) (int, string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/clipboard/type":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"type": "image", "format": "png"})
		case "/clipboard/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imagePayload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse mock URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse mock port: %v", err)
	}

	tokenFile := filepath.Join(tmpDir, "session.token")
	if err := os.WriteFile(tokenFile, []byte("test-token\n"), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return port, tokenFile
}

// TestShimInterceptsMatchingInvocations runs the generated bash shim against a
// real mock HTTP daemon. For each case where the pattern *should* match, it
// asserts that the shim's stdout contains the daemon's response (proving the
// HTTP path was taken) and that fallback was NOT invoked. For unmatched
// patterns, it asserts the opposite: stdout is empty of the daemon payload
// and fallback captured the original args.
//
// This distinguishes "pattern matched -> probe succeeded -> daemon returned
// data" from "pattern didn't match -> default fallback", which a daemon-down
// variant cannot do.
func TestShimInterceptsMatchingInvocations(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	type expectation int
	const (
		expectInterceptImage expectation = iota // stdout == imagePayload, no fallback
		expectInterceptType                     // stdout contains expectedTypeToken, no fallback
		expectFallback                          // stdout empty of payload, fallback captures args
	)

	cases := []struct {
		name   string
		render func(port int, real string) string
		args   []string
		expect expectation
	}{
		{
			name:   "claude_xclip_targets",
			render: XclipShim,
			args:   []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
			expect: expectInterceptType,
		},
		{
			name:   "claude_xclip_image",
			render: XclipShim,
			args:   []string{"-selection", "clipboard", "-t", "image/png", "-o"},
			expect: expectInterceptImage,
		},
		{
			name:   "opencode_xclip_image",
			render: XclipShim,
			args:   []string{"-selection", "clipboard", "-t", "image/png", "-o"},
			expect: expectInterceptImage,
		},
		{
			name:   "xclip_passthrough_unrelated",
			render: XclipShim,
			args:   []string{"-selection", "primary", "-o"},
			expect: expectFallback,
		},
		{
			name:   "claude_wlpaste_list_types",
			render: WlPasteShim,
			args:   []string{"--list-types"},
			expect: expectInterceptType,
		},
		{
			name:   "claude_wlpaste_type_long",
			render: WlPasteShim,
			args:   []string{"--type", "image/png"},
			expect: expectInterceptImage,
		},
		{
			name:   "opencode_wlpaste_type_short",
			render: WlPasteShim,
			args:   []string{"-t", "image/png"},
			expect: expectInterceptImage,
		},
		{
			name:   "wlpaste_passthrough_unrelated",
			render: WlPasteShim,
			args:   []string{"--watch"},
			expect: expectFallback,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			sentinel := filepath.Join(tmpDir, "fallback.log")

			// Fake "real" binary records its args to a sentinel and exits 0.
			realBin := filepath.Join(tmpDir, "fake-real")
			fakeScript := "#!/bin/bash\n" +
				"printf '%s\\n' \"$*\" > \"" + sentinel + "\"\n" +
				"exit 0\n"
			if err := os.WriteFile(realBin, []byte(fakeScript), 0755); err != nil {
				t.Fatalf("write fake real: %v", err)
			}

			port, tokenFile := startMockDaemon(t, tmpDir)

			shim := tc.render(port, realBin)
			shimPath := filepath.Join(tmpDir, "shim.sh")
			if err := os.WriteFile(shimPath, []byte(shim), 0755); err != nil {
				t.Fatalf("write shim: %v", err)
			}

			cmd := exec.Command("bash", append([]string{shimPath}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"CC_CLIP_PORT="+strconv.Itoa(port),
				"CC_CLIP_TOKEN_FILE="+tokenFile,
				"CC_CLIP_PROBE_TIMEOUT_MS=2000",
				"CC_CLIP_FETCH_TIMEOUT_MS=5000",
			)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("shim execution failed: %v (stdout=%q)", err, string(out))
			}

			fallbackInvoked := false
			var recordedArgs string
			if recorded, readErr := os.ReadFile(sentinel); readErr == nil {
				fallbackInvoked = true
				recordedArgs = strings.TrimSpace(string(recorded))
			}

			switch tc.expect {
			case expectInterceptImage:
				if fallbackInvoked {
					t.Fatalf("expected interception, but fallback was invoked with %q (stdout=%q)",
						recordedArgs, string(out))
				}
				if !strings.Contains(string(out), string(imagePayload)) {
					t.Fatalf("expected stdout to contain daemon image payload %q, got %q",
						string(imagePayload), string(out))
				}
			case expectInterceptType:
				if fallbackInvoked {
					t.Fatalf("expected interception, but fallback was invoked with %q (stdout=%q)",
						recordedArgs, string(out))
				}
				if !strings.Contains(string(out), expectedTypeToken) {
					t.Fatalf("expected stdout to contain %q, got %q", expectedTypeToken, string(out))
				}
			case expectFallback:
				if !fallbackInvoked {
					t.Fatalf("expected fallback invocation, but sentinel was absent (stdout=%q)", string(out))
				}
				want := strings.Join(tc.args, " ")
				if recordedArgs != want {
					t.Fatalf("fallback args mismatch\n  got:  %q\n  want: %q", recordedArgs, want)
				}
				if strings.Contains(string(out), string(imagePayload)) {
					t.Fatalf("fallback path unexpectedly produced daemon payload: %q", string(out))
				}
			}
		})
	}
}
