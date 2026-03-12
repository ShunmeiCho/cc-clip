package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestStopLocalProcessDoesNotKillUnexpectedCommand(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatalf("failed to write pid file: %v", err)
	}

	stopLocalProcess(pidFile, "Xvfb")
	time.Sleep(100 * time.Millisecond)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case err := <-waitDone:
		t.Fatalf("unexpected command should still be running, but exited early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after stale pid detection, got err=%v", err)
	}
}
