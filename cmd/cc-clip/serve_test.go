package main

import (
	"bytes"
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

func TestServeRotateTokenLeavesTokenUntouchedWhenPortTaken(t *testing.T) {
	// cmdServe uses log.Fatal on bind failure, so run it in a child process.
	if os.Getenv("CC_CLIP_TEST_SERVE_CHILD") == "1" {
		os.Args = []string{"cc-clip", "serve", "--rotate-token"}
		cmdServe()
		return
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CC_CLIP_TOKEN_DIR", home)
	t.Setenv("CC_CLIP_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	t.Setenv("CC_CLIP_TEST_SERVE_CHILD", "1")
	path := filepath.Join(home, "session.token")
	before := []byte("running-daemon-token\n2099-01-01T00:00:00Z\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	// An old timestamp also catches an otherwise byte-identical rewrite.
	stamp := time.Unix(946684800, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServeRotateTokenLeavesTokenUntouchedWhenPortTaken$")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("serve child timed out: %s", out)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("serve child error = %v, want exit 1; output: %s", err, out)
	}
	if !strings.Contains(string(out), "failed to listen on "+listener.Addr().String()) {
		t.Fatalf("expected bind failure, got: %s", out)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("serve --rotate-token changed the shared token file despite losing the port race")
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, current) || !current.ModTime().Equal(original.ModTime()) || current.Mode() != original.Mode() {
		t.Fatal("serve --rotate-token replaced or modified the shared token file despite losing the port race")
	}
}

func TestLoadInstanceIDOrUnavailablePreservesDegradedDaemon(t *testing.T) {
	for _, content := range []string{"", "corrupt-instance-id"} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "instance-id")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var warning bytes.Buffer
			oldWriter := log.Writer()
			log.SetOutput(&warning)
			t.Cleanup(func() { log.SetOutput(oldWriter) })
			id := loadInstanceIDOrUnavailable(dir)
			if id != "" {
				t.Fatalf("loadInstanceIDOrUnavailable() = %q, want unavailable identity", id)
			}
			for _, hint := range []string{"WARN: tunnel identity unavailable", strconv.Quote(path), "restore a valid backup", "move this file aside and restart", "daemon continues", "docs/troubleshooting.md#tunnel-identity-unavailable"} {
				if !strings.Contains(warning.String(), hint) {
					t.Fatalf("warning missing %q: %s", hint, &warning)
				}
			}
			if content != "" && strings.Contains(warning.String(), content) {
				t.Fatal("warning leaked corrupt file contents")
			}

			tm := token.NewManager(time.Hour)
			sess, err := tm.Generate()
			if err != nil {
				t.Fatal(err)
			}
			srv := daemon.NewServer("127.0.0.1:0", nil, tm, session.NewStore(time.Hour))
			srv.SetInstanceID(id)
			for _, check := range []struct {
				path   string
				bearer string
				status int
			}{
				{"/health", "", http.StatusOK},
				{"/tunnel/identity", "", http.StatusUnauthorized},
				{"/tunnel/identity", "wrong-token", http.StatusUnauthorized},
				{"/tunnel/identity", sess.Token, http.StatusServiceUnavailable},
			} {
				req := httptest.NewRequest(http.MethodGet, check.path, nil)
				req.Header.Set("User-Agent", "cc-clip")
				if check.bearer != "" {
					req.Header.Set("Authorization", "Bearer "+check.bearer)
				}
				w := httptest.NewRecorder()
				srv.Handler().ServeHTTP(w, req)
				if w.Code != check.status {
					t.Fatalf("%s status = %d, want %d", check.path, w.Code, check.status)
				}
			}
		})
	}
}
