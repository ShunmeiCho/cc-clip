package shim

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// writeMockDaemon records POST /clipboard/text bodies so tests can assert the
// forwarded payload byte-for-byte.
type writeMockDaemon struct {
	mu     sync.Mutex
	bodies []string
}

func (m *writeMockDaemon) posts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

func startWriteMockDaemon(t *testing.T) (*writeMockDaemon, int) {
	t.Helper()
	m := &writeMockDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/clipboard/text" {
			b := make([]byte, 0, 1024)
			buf := make([]byte, 4096)
			for {
				n, err := r.Body.Read(buf)
				b = append(b, buf[:n]...)
				if err != nil {
					break
				}
			}
			m.mu.Lock()
			m.bodies = append(m.bodies, string(b))
			m.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return m, port
}

// writeFakeReal writes a stand-in real binary that records argv and stdin.
func writeFakeReal(t *testing.T, dir, name string) (bin, argvLog, stdinLog string) {
	t.Helper()
	argvLog = filepath.Join(dir, name+".argv")
	stdinLog = filepath.Join(dir, name+".stdin")
	bin = filepath.Join(dir, name)
	script := "#!/bin/bash\nprintf '%s\\n' \"$*\" > " + bashPath(argvLog) + "\ncat > " + bashPath(stdinLog) + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create logs so "never invoked" is distinguishable from "invoked
	// with empty input".
	for _, f := range []string{argvLog, stdinLog} {
		if err := os.WriteFile(f, []byte("<never-invoked>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return bin, argvLog, stdinLog
}

type writeShimRun struct {
	daemon   *writeMockDaemon
	argvLog  string
	stdinLog string
	exitCode int
	output   string
}

// runWriteShim renders a shim (with realName as the fake real binary), feeds
// stdin, and executes it against a mock daemon (or a closed port when
// daemonUp=false).
func runWriteShim(t *testing.T, render func(port int, real string) string, realName string, realExists, daemonUp bool, stdin string, args ...string) writeShimRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash semantics not reliable on windows test dirs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmp := t.TempDir()
	daemon, port := startWriteMockDaemon(t)
	if !daemonUp {
		// Grab a port with nothing listening.
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skip("cannot bind loopback")
		}
		port = l.Addr().(*net.TCPAddr).Port
		l.Close()
	}

	tokenFile := filepath.Join(tmp, "session.token")
	if err := os.WriteFile(tokenFile, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	realBin, argvLog, stdinLog := writeFakeReal(t, tmp, realName)
	realPath := bashPath(realBin)
	if !realExists {
		os.Remove(realBin)
		realPath = bashPath(filepath.Join(tmp, "no-such-real"))
	}

	shimPath := filepath.Join(tmp, "shim.sh")
	if err := os.WriteFile(shimPath, []byte(render(port, realPath)), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", append([]string{bashPath(shimPath)}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	// PATH deliberately excludes the tmp dir so fallback resolution cannot
	// find a real binary by search when realExists=false.
	cmd.Env = append(os.Environ(),
		"PATH=/usr/bin:/bin",
		"CC_CLIP_PORT="+strconv.Itoa(port),
		"CC_CLIP_TOKEN_FILE="+bashPath(tokenFile),
		"CC_CLIP_PROBE_TIMEOUT_MS=2000",
		"CC_CLIP_FETCH_TIMEOUT_MS=5000",
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("shim execution failed: %v output=%q", err, out)
		}
	}
	return writeShimRun{daemon: daemon, argvLog: argvLog, stdinLog: stdinLog, exitCode: code, output: string(out)}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestXclipShimWriteDualWrite pins #128 phase 2 on X11: a clipboard write is
// forwarded to the local daemon byte-for-byte AND replayed into the real
// xclip with identical stdin and argv, so remote behavior is unchanged.
func TestXclipShimWriteDualWrite(t *testing.T) {
	payload := "line one\nlong command --with flags\ntrailing newline kept\n"
	r := runWriteShim(t, XclipShim, "xclip", true, true, payload, "-selection", "clipboard")

	if r.exitCode != 0 {
		t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
	}
	posts := r.daemon.posts()
	if len(posts) != 1 || posts[0] != payload {
		t.Fatalf("daemon got %q, want exactly %q", posts, payload)
	}
	if got := readLog(t, r.stdinLog); got != payload {
		t.Fatalf("real xclip stdin %q, want %q", got, payload)
	}
	if got := readLog(t, r.argvLog); !strings.Contains(got, "-selection clipboard") {
		t.Fatalf("real xclip argv %q must keep the original args", got)
	}
}

// TestXclipShimWriteTunnelDownStillCopiesRemotely: the local forward is
// best-effort; with the daemon unreachable the remote clipboard write must
// succeed exactly as without the shim.
func TestXclipShimWriteTunnelDownStillCopiesRemotely(t *testing.T) {
	payload := "remote-only\n"
	r := runWriteShim(t, XclipShim, "xclip", true, false, payload, "-selection", "clipboard", "-i")

	if r.exitCode != 0 {
		t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
	}
	if got := readLog(t, r.stdinLog); got != payload {
		t.Fatalf("real xclip stdin %q, want %q", got, payload)
	}
}

// TestXclipShimWriteHeadlessForwardOnly: no real xclip at all (headless box).
// The forward alone must count as success — this is the primary use case.
func TestXclipShimWriteHeadlessForwardOnly(t *testing.T) {
	payload := "headless copy\n"
	r := runWriteShim(t, XclipShim, "xclip", false, true, payload, "-selection", "clipboard")

	if r.exitCode != 0 {
		t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
	}
	posts := r.daemon.posts()
	if len(posts) != 1 || posts[0] != payload {
		t.Fatalf("daemon got %q, want %q", posts, payload)
	}
}

// TestXclipShimWriteShapeGates: primary-selection writes and unrecognized
// read shapes must never be forwarded.
func TestXclipShimWriteShapeGates(t *testing.T) {
	t.Run("primary selection passes through untouched", func(t *testing.T) {
		r := runWriteShim(t, XclipShim, "xclip", true, true, "x\n", "-selection", "primary")
		if r.exitCode != 0 {
			t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
		}
		if got := r.daemon.posts(); len(got) != 0 {
			t.Fatalf("primary write must not be forwarded: %q", got)
		}
		if got := readLog(t, r.stdinLog); got != "x\n" {
			t.Fatalf("real must receive the payload, got %q", got)
		}
	})
	t.Run("unrecognized read shape falls back without forwarding", func(t *testing.T) {
		r := runWriteShim(t, XclipShim, "xclip", true, true, "", "-selection", "clipboard", "-t", "text/html", "-o")
		if r.exitCode != 0 {
			t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
		}
		if got := r.daemon.posts(); len(got) != 0 {
			t.Fatalf("read shape must not be forwarded: %q", got)
		}
		if got := readLog(t, r.argvLog); !strings.Contains(got, "-t text/html -o") {
			t.Fatalf("fallback must exec the real binary with original args, got %q", got)
		}
	})
}

// TestWlCopyShimWrites pins the Wayland write-side companion.
func TestWlCopyShimWrites(t *testing.T) {
	t.Run("stdin form dual-writes byte-exact", func(t *testing.T) {
		payload := "wayland\ncopy\n"
		r := runWriteShim(t, WlCopyShim, "wl-copy", true, true, payload)
		if r.exitCode != 0 {
			t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
		}
		if posts := r.daemon.posts(); len(posts) != 1 || posts[0] != payload {
			t.Fatalf("daemon got %q, want %q", posts, payload)
		}
		if got := readLog(t, r.stdinLog); got != payload {
			t.Fatalf("real wl-copy stdin %q, want %q", got, payload)
		}
	})
	t.Run("argument form joins with spaces, no trailing newline", func(t *testing.T) {
		r := runWriteShim(t, WlCopyShim, "wl-copy", true, true, "", "hello", "wrapped", "world")
		if r.exitCode != 0 {
			t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
		}
		if posts := r.daemon.posts(); len(posts) != 1 || posts[0] != "hello wrapped world" {
			t.Fatalf("daemon got %q, want %q", posts, "hello wrapped world")
		}
		if got := readLog(t, r.argvLog); !strings.Contains(got, "hello wrapped world") {
			t.Fatalf("real wl-copy argv %q must keep the text args", got)
		}
	})
	t.Run("trim-newline mirrors on the forwarded payload", func(t *testing.T) {
		r := runWriteShim(t, WlCopyShim, "wl-copy", true, true, "trimmed\n", "-n")
		if r.exitCode != 0 {
			t.Fatalf("exit=%d output=%q", r.exitCode, r.output)
		}
		if posts := r.daemon.posts(); len(posts) != 1 || posts[0] != "trimmed" {
			t.Fatalf("daemon got %q, want %q (trailing newline trimmed)", posts, "trimmed")
		}
		// The real binary applies its own trim, so it receives the original.
		if got := readLog(t, r.stdinLog); got != "trimmed\n" {
			t.Fatalf("real wl-copy stdin %q, want original bytes", got)
		}
	})
	t.Run("primary and non-text types pass through untouched", func(t *testing.T) {
		for _, args := range [][]string{
			{"--primary"},
			{"-t", "image/png"},
			{"--clear"},
		} {
			r := runWriteShim(t, WlCopyShim, "wl-copy", true, true, "p\n", args...)
			if r.exitCode != 0 {
				t.Fatalf("args %v: exit=%d output=%q", args, r.exitCode, r.output)
			}
			if got := r.daemon.posts(); len(got) != 0 {
				t.Fatalf("args %v must not be forwarded: %q", args, got)
			}
		}
	})
}

// TestInstallWaylandShipsWlCopyCompanion: the wayland target installs and
// uninstalls the write-side shim alongside wl-paste.
func TestInstallWaylandShipsWlCopyCompanion(t *testing.T) {
	dir := t.TempDir()
	res, err := Install(TargetWlPaste, dir, 18339)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Target != TargetWlPaste {
		t.Fatalf("target = %v", res.Target)
	}
	wlCopy := filepath.Join(dir, "wl-copy")
	data, err := os.ReadFile(wlCopy)
	if err != nil {
		t.Fatalf("wl-copy companion not installed: %v", err)
	}
	if !strings.Contains(string(data), "cc-clip wl-copy shim") {
		t.Fatalf("companion content unexpected: %.80s", data)
	}

	if err := Uninstall(TargetWlPaste, dir); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(wlCopy); !os.IsNotExist(err) {
		t.Fatalf("wl-copy companion must be removed with its sibling, stat err=%v", err)
	}
}
