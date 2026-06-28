package shim

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// probeErrExecutor is a SessionExecutor whose calls always fail. It exists
// because errorExecutor (pathfix_test.go) implements only Exec, not the full
// SessionExecutor interface that RemoteClaudeProbe accepts.
type probeErrExecutor struct{ err error }

func (e *probeErrExecutor) Exec(string) (string, error) { return "", e.err }

func (e *probeErrExecutor) ExecWithStdin(string, io.Reader) (string, error) { return "", e.err }

// TestRemoteClaudeProbe verifies the tool-free presence probe used as the
// fail-safe classifier for the N0 gate. It must report "present" for any
// existing claude (regular file or even a broken symlink) so a remote that
// merely cannot run the full detector is never mistaken for a fresh one.
func TestRemoteClaudeProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}

	t.Run("absent", func(t *testing.T) {
		home, _ := setupFakeHome(t)
		exists, err := RemoteClaudeProbe(&localSession{home: home})
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if exists {
			t.Fatal("expected absent on a fresh home")
		}
	})

	t.Run("present_regular_file", func(t *testing.T) {
		home, _ := setupFakeHome(t)
		bin := filepath.Join(home, ".local", "bin", "claude")
		if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		exists, err := RemoteClaudeProbe(&localSession{home: home})
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if !exists {
			t.Fatal("expected present for a regular claude file")
		}
	})

	t.Run("present_broken_symlink", func(t *testing.T) {
		home, _ := setupFakeHome(t)
		link := filepath.Join(home, ".local", "bin", "claude")
		if err := os.Symlink(filepath.Join(home, "does-not-exist"), link); err != nil {
			t.Fatal(err)
		}
		exists, err := RemoteClaudeProbe(&localSession{home: home})
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if !exists {
			t.Fatal("expected present for a broken symlink (must not be treated as fresh)")
		}
	})

	t.Run("exec_error_propagates", func(t *testing.T) {
		if _, err := RemoteClaudeProbe(&probeErrExecutor{err: errors.New("ssh down")}); err == nil {
			t.Fatal("expected error to propagate when Exec fails")
		}
	})
}
