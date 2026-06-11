package shim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if !strings.Contains(got, "_cc_clip_resolve_real_xclip") {
		t.Fatalf("xclip fallback resolver missing: %q", got)
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
	if !strings.Contains(got, "_cc_clip_resolve_real_wl_paste") {
		t.Fatalf("wl-paste fallback resolver missing: %q", got)
	}
}

func TestShimsIncludeTextClipboardEndpoint(t *testing.T) {
	for name, content := range map[string]string{
		"xclip":    XclipShim(18339, "/usr/bin/xclip"),
		"wl-paste": WlPasteShim(18339, "/usr/bin/wl-paste"),
	} {
		if !strings.Contains(content, `"/clipboard/text"`) {
			t.Fatalf("%s shim must fetch /clipboard/text", name)
		}
		if !strings.Contains(content, `"text"`) {
			t.Fatalf("%s shim must handle text clipboard type", name)
		}
	}
}

// imagePayload is a distinctive byte sequence served by the mock daemon for
// /clipboard/image. Tests assert that when a shim pattern actually intercepts
// an invocation, this exact payload reaches stdout — which is only possible if
// the shim went through the HTTP path rather than falling through to the real
// binary.
var imagePayload = []byte("CC-CLIP-INTERCEPTED-PAYLOAD")
var textPayload = []byte("cc-clip intercepted text\n")

const expectedTypeToken = "image/png"
const expectedTextTypeToken = "UTF8_STRING"

func bashPath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS != "windows" {
		return path
	}
	vol := filepath.VolumeName(path)
	if len(vol) == 2 && vol[1] == ':' {
		drive := strings.ToLower(vol[:1])
		rest := strings.TrimPrefix(path[len(vol):], `\`)
		if exec.Command("bash", "-lc", "test -d /mnt/"+drive).Run() == nil {
			return "/mnt/" + drive + "/" + strings.ReplaceAll(rest, `\`, "/")
		}
		return "/" + drive + "/" + strings.ReplaceAll(rest, `\`, "/")
	}
	return strings.ReplaceAll(path, `\`, "/")
}

func bashPATH(paths ...string) string {
	parts := make([]string, 0, len(paths)+1)
	for _, p := range paths {
		parts = append(parts, bashPath(p))
	}
	parts = append(parts, os.Getenv("PATH"))
	if runtime.GOOS == "windows" {
		return strings.Join(parts, ":")
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// startMockDaemon serves the two endpoints the shim consumes when
// intercepting: /clipboard/type (JSON `{"type":"image","format":"png"}`) and
// /clipboard/image (raw bytes). Returns (port, tokenFilePath).
func startMockDaemon(t *testing.T, tmpDir string) (int, string) {
	return startMockDaemonWithType(t, tmpDir, "image")
}

func startMockDaemonWithType(t *testing.T, tmpDir string, clipType string) (int, string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/clipboard/type":
			w.Header().Set("Content-Type", "application/json")
			if clipType == "text" {
				_ = json.NewEncoder(w).Encode(map[string]string{"type": "text"})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]string{"type": "image", "format": "png"})
			}
		case "/clipboard/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imagePayload)
		case "/clipboard/text":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(textPayload)
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

func writeFakeCurl(t *testing.T, dir string) (argvLog, stdinLog string) {
	return writeFakeCurlWithType(t, dir, "image")
}

func writeFakeCurlWithType(t *testing.T, dir string, clipType string) (argvLog, stdinLog string) {
	t.Helper()

	argvLog = filepath.Join(dir, "curl-argv.log")
	stdinLog = filepath.Join(dir, "curl-stdin.log")
	dataLog := filepath.Join(dir, "curl-data.log")
	curlPath := filepath.Join(dir, "curl")
	typeJSON := `{"type":"image","format":"png"}`
	if clipType == "text" {
		typeJSON = `{"type":"text"}`
	}
	script := "#!/bin/bash\n" +
		"set -euo pipefail\n" +
		"argv_log=" + strconv.Quote(bashPath(argvLog)) + "\n" +
		"stdin_log=" + strconv.Quote(bashPath(stdinLog)) + "\n" +
		"data_log=" + strconv.Quote(bashPath(dataLog)) + "\n" +
		"printf '%s\\n' \"$*\" > \"$argv_log\"\n" +
		"for ((i=1; i<=$#; i++)); do\n" +
		"  if [ \"${!i}\" = \"-K\" ]; then\n" +
		"    next=$((i+1))\n" +
		"    if [ \"${!next}\" = \"-\" ]; then\n" +
		"      cat > \"$stdin_log\"\n" +
		"      break\n" +
		"    fi\n" +
		"  fi\n" +
		"done\n" +
		"outfile=\"\"\n" +
		"prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then outfile=\"$arg\"; fi\n" +
		"  case \"$prev\" in\n" +
		"    -d|--data|--data-raw|--data-binary)\n" +
		"      case \"$arg\" in @*) cat \"${arg#@}\" > \"$data_log\" ;; *) printf '%s' \"$arg\" > \"$data_log\" ;; esac\n" +
		"      ;;\n" +
		"  esac\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"url=\"${@: -1}\"\n" +
		"case \"$url\" in\n" +
		"  */clipboard/type) printf " + strconv.Quote(typeJSON+"\n") + " ;;\n" +
		"  */clipboard/image) if [ -n \"$outfile\" ]; then printf " + strconv.Quote(string(imagePayload)) + " > \"$outfile\"; else printf " + strconv.Quote(string(imagePayload)) + "; fi ;;\n" +
		"  */clipboard/text) if [ -n \"$outfile\" ]; then printf " + strconv.Quote(string(textPayload)) + " > \"$outfile\"; else printf " + strconv.Quote(string(textPayload)) + "; fi ;;\n" +
		"  */notify) printf '204' ;;\n" +
		"esac\n"
	if err := os.WriteFile(curlPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	for _, name := range []string{"timeout", "nc"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/bash\nexit 0\n"), 0755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return argvLog, stdinLog
}

func TestShimsKeepTokenOutOfCurlArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash path and executable semantics are not reliable from Windows test temp dirs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	cases := []struct {
		name   string
		render func(port int, real string) string
		args   []string
	}{
		{
			name:   "xclip_json",
			render: XclipShim,
			args:   []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
		},
		{
			name:   "xclip_binary",
			render: XclipShim,
			args:   []string{"-selection", "clipboard", "-t", "image/png", "-o"},
		},
		{
			name:   "xclip_text",
			render: XclipShim,
			args:   []string{"-selection", "clipboard", "-o"},
		},
		{
			name:   "wlpaste_json",
			render: WlPasteShim,
			args:   []string{"--list-types"},
		},
		{
			name:   "wlpaste_binary",
			render: WlPasteShim,
			args:   []string{"--type", "image/png"},
		},
		{
			name:   "wlpaste_text",
			render: WlPasteShim,
			args:   []string{"--type", "text/plain"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			argvLog, stdinLog := writeFakeCurl(t, tmpDir)
			port, tokenFile := startMockDaemon(t, tmpDir)

			token := "secret-token-must-not-appear-in-argv"
			if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0600); err != nil {
				t.Fatalf("write token file: %v", err)
			}

			realBin := filepath.Join(tmpDir, "fake-real")
			if err := os.WriteFile(realBin, []byte("#!/bin/bash\nexit 99\n"), 0755); err != nil {
				t.Fatalf("write fake real: %v", err)
			}

			shimPath := filepath.Join(tmpDir, "shim.sh")
			if err := os.WriteFile(shimPath, []byte(tc.render(port, bashPath(realBin))), 0755); err != nil {
				t.Fatalf("write shim: %v", err)
			}

			cmd := exec.Command("bash", append([]string{bashPath(shimPath)}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"PATH="+bashPATH(tmpDir),
				"CC_CLIP_PORT="+strconv.Itoa(port),
				"CC_CLIP_TOKEN_FILE="+bashPath(tokenFile),
				"CC_CLIP_PROBE_TIMEOUT_MS=2000",
				"CC_CLIP_FETCH_TIMEOUT_MS=5000",
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("shim execution failed: %v output=%q", err, string(out))
			}

			argv, err := os.ReadFile(argvLog)
			if err != nil {
				t.Fatalf("read fake curl argv log: %v", err)
			}
			if strings.Contains(string(argv), token) {
				t.Fatalf("token leaked through curl argv: %q", string(argv))
			}

			stdinConfig, err := os.ReadFile(stdinLog)
			if err != nil {
				t.Fatalf("read fake curl stdin config: %v", err)
			}
			if !strings.Contains(string(stdinConfig), token) {
				t.Fatalf("expected token in curl stdin config, got %q", string(stdinConfig))
			}
		})
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("bash path and executable semantics are not reliable from Windows test temp dirs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	type expectation int
	const (
		expectInterceptImage    expectation = iota // stdout == imagePayload, no fallback
		expectInterceptType                        // stdout contains expectedTypeToken, no fallback
		expectInterceptText                        // stdout == textPayload, no fallback
		expectInterceptTextType                    // stdout contains expectedTextTypeToken, no fallback
		expectFallback                             // stdout empty of payload, fallback captures args
	)

	cases := []struct {
		name     string
		render   func(port int, real string) string
		args     []string
		expect   expectation
		clipType string
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
			name:     "xclip_targets_text",
			render:   XclipShim,
			args:     []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
			expect:   expectInterceptTextType,
			clipType: "text",
		},
		{
			name:     "xclip_text",
			render:   XclipShim,
			args:     []string{"-selection", "clipboard", "-o"},
			expect:   expectInterceptText,
			clipType: "text",
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
		{
			name:     "wlpaste_list_types_text",
			render:   WlPasteShim,
			args:     []string{"--list-types"},
			expect:   expectInterceptTextType,
			clipType: "text",
		},
		{
			name:     "wlpaste_type_text",
			render:   WlPasteShim,
			args:     []string{"--type", "text/plain"},
			expect:   expectInterceptText,
			clipType: "text",
		},
		{
			name:     "wlpaste_default_text",
			render:   WlPasteShim,
			args:     []string{},
			expect:   expectInterceptText,
			clipType: "text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			sentinel := filepath.Join(tmpDir, "fallback.log")
			clipType := tc.clipType
			if clipType == "" {
				clipType = "image"
			}
			_, _ = writeFakeCurlWithType(t, tmpDir, clipType)

			// Fake "real" binary records its args to a sentinel and exits 0.
			realBin := filepath.Join(tmpDir, "fake-real")
			fakeScript := "#!/bin/bash\n" +
				"printf '%s\\n' \"$*\" > \"" + bashPath(sentinel) + "\"\n" +
				"exit 0\n"
			if err := os.WriteFile(realBin, []byte(fakeScript), 0755); err != nil {
				t.Fatalf("write fake real: %v", err)
			}

			port, tokenFile := startMockDaemonWithType(t, tmpDir, clipType)

			shim := tc.render(port, bashPath(realBin))
			shimPath := filepath.Join(tmpDir, "shim.sh")
			if err := os.WriteFile(shimPath, []byte(shim), 0755); err != nil {
				t.Fatalf("write shim: %v", err)
			}

			cmd := exec.Command("bash", append([]string{bashPath(shimPath)}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"PATH="+bashPATH(tmpDir),
				"CC_CLIP_PORT="+strconv.Itoa(port),
				"CC_CLIP_TOKEN_FILE="+bashPath(tokenFile),
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
			case expectInterceptText:
				if fallbackInvoked {
					t.Fatalf("expected interception, but fallback was invoked with %q (stdout=%q)",
						recordedArgs, string(out))
				}
				if string(out) != string(textPayload) {
					t.Fatalf("expected stdout to equal daemon text payload %q, got %q",
						string(textPayload), string(out))
				}
			case expectInterceptTextType:
				if fallbackInvoked {
					t.Fatalf("expected interception, but fallback was invoked with %q (stdout=%q)",
						recordedArgs, string(out))
				}
				if !strings.Contains(string(out), expectedTextTypeToken) && !strings.Contains(string(out), "text/plain") {
					t.Fatalf("expected stdout to contain text target, got %q", string(out))
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

func TestXclipShimFallbackResolvesRealBinaryFromPathWhenConfiguredPathIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash path and executable semantics are not reliable from Windows test temp dirs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	sentinel := filepath.Join(tmpDir, "fallback.log")
	realBin := filepath.Join(realDir, "xclip")
	fakeScript := "#!/bin/bash\n" +
		"printf '%s\\n' \"$*\" > \"" + bashPath(sentinel) + "\"\n" +
		"exit 0\n"
	if err := os.WriteFile(realBin, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("write fake real xclip: %v", err)
	}

	shimPath := filepath.Join(tmpDir, "xclip")
	if err := os.WriteFile(shimPath, []byte(XclipShim(18339, "/missing/xclip")), 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	cmd := exec.Command("bash", bashPath(shimPath), "-selection", "primary", "-o")
	cmd.Env = append(os.Environ(), "PATH="+bashPATH(tmpDir, realDir))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("shim fallback failed: %v (stdout=%q)", err, string(out))
	}
	recorded, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("fallback sentinel missing: %v", err)
	}
	if got, want := strings.TrimSpace(string(recorded)), "-selection primary -o"; got != want {
		t.Fatalf("fallback args mismatch\n  got:  %q\n  want: %q", got, want)
	}
}

func TestXclipShimFallbackFailsClearlyWhenRealBinaryIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash path and executable semantics are not reliable from Windows test temp dirs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmpDir := t.TempDir()
	shimPath := filepath.Join(tmpDir, "xclip")
	if err := os.WriteFile(shimPath, []byte(XclipShim(18339, "/missing/xclip")), 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	cmd := exec.Command("bash", bashPath(shimPath), "-selection", "primary", "-o")
	cmd.Env = append(os.Environ(), "PATH="+bashPATH(tmpDir))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected fallback failure when real xclip is absent, got success: %q", string(out))
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 127 {
		t.Fatalf("expected exit 127, got err=%v output=%q", err, string(out))
	}
	if !strings.Contains(string(out), "real xclip binary not found") {
		t.Fatalf("expected clear missing-binary error, got %q", string(out))
	}
}
