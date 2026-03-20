package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestStopLocalProcessDoesNotKillUnexpectedCommand(t *testing.T) {
	cmd := helperSleepProcess(t)
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

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-1] != "sleep-helper" {
		os.Exit(0)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func helperSleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()

	args := []string{"-test.run=TestHelperProcess", "--", "sleep-helper"}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return cmd
}

func TestReleaseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0.3.0", "0.3.0"},
		{"0.3.0-1-g99b1298", "0.3.0"},
		{"0.3.0-15-gabcdef0", "0.3.0"},
		{"1.0.0-rc1", "1.0.0-rc1"},              // pre-release tag, not git describe
		{"1.0.0-rc1-3-g1234567", "1.0.0-rc1"},   // git describe from pre-release tag
		{"0.3.0-beta-2-gabcdef0", "0.3.0-beta"}, // git describe from tag with dash
		{"dev", "dev"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := releaseVersion(tt.input)
			if got != tt.want {
				t.Errorf("releaseVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"0", true},
		{"123", true},
		{"", false},
		{"abc", false},
		{"12a", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isNumeric(tt.input)
			if got != tt.want {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
