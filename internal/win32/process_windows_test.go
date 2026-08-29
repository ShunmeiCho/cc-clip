//go:build windows

package win32

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProcessImageName_Self is the load-bearing case: a live process must be
// identifiable without asking PowerShell for its command line. The bug this
// guards (issue #140) was that an unreadable command line was treated as
// "process is not ours", which deleted the hotkey PID file.
func TestProcessImageName_Self(t *testing.T) {
	name, err := ProcessImageName(os.Getpid())
	if err != nil {
		t.Fatalf("ProcessImageName(self) failed: %v", err)
	}
	if !filepath.IsAbs(name) {
		t.Errorf("expected an absolute image path, got %q", name)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".exe") {
		t.Errorf("expected an .exe image path, got %q", name)
	}
}

// TestProcessImageName_Exited pins the distinction the caller depends on: a
// PID that no longer exists must report ErrProcessNotFound specifically, not a
// generic error. Only that answer justifies discarding a recorded PID.
func TestProcessImageName_Exited(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process failed: %v", err)
	}

	_, err := ProcessImageName(pid)
	if !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("expected ErrProcessNotFound for exited pid %d, got %v", pid, err)
	}
}

func TestProcessImageName_RejectsNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := ProcessImageName(pid); !errors.Is(err, ErrProcessNotFound) {
			t.Errorf("ProcessImageName(%d) = %v, want ErrProcessNotFound", pid, err)
		}
	}
}
