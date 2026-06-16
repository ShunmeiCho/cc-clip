package shim

import (
	"io"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/testshell"
)

// localSession is a SessionExecutor that runs commands locally via bash -c
// against a configured $HOME (a t.TempDir() in tests). $HOME is set per
// command so the bash `~` expansion lands in the fake home.
type localSession struct {
	home string
}

func (l *localSession) Exec(cmd string) (string, error) {
	c := testshell.Command(l.home, cmd)
	out, err := c.Output() // stdout only, matches *SSHSession.Exec semantics
	return strings.TrimSpace(string(out)), err
}

func (l *localSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
	c := testshell.Command(l.home, cmd)
	c.Stdin = stdin
	out, err := c.CombinedOutput() // combined, matches *SSHSession.ExecWithStdin
	return string(out), err
}

func TestLocalSession_ExecStdoutOnly(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	out, err := s.Exec("echo out; echo err >&2")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if out != "out" {
		t.Fatalf("expected stdout %q, got %q (stderr should be discarded)", "out", out)
	}
}

func TestLocalSession_ExecWithStdinCombined(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	out, err := s.ExecWithStdin("echo out; echo err >&2", strings.NewReader(""))
	if err != nil {
		t.Fatalf("ExecWithStdin failed: %v", err)
	}
	if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
		t.Fatalf("expected combined output containing both 'out' and 'err', got %q", out)
	}
}

func TestLocalSession_ImplementsSessionExecutor(t *testing.T) {
	var _ SessionExecutor = (*localSession)(nil)
}
