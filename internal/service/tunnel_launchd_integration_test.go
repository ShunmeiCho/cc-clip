//go:build darwin

package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt in because these jobs run in the user's real GUI launchd domain. Their
// executable is an isolated local fixture; no SSH, daemon or credentials are used.
func TestTunnelLaunchAgentRealLifecycle(t *testing.T) {
	if os.Getenv("CC_CLIP_TEST_LAUNCHD") != "1" {
		t.Skip("set CC_CLIP_TEST_LAUNCHD=1 to exercise the real GUI launchd domain")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	manager, err := NewTunnelManager()
	if err != nil {
		t.Fatal(err)
	}
	m := manager.(*tunnelLaunchd)
	// A unique literal target gives the fixture its own label in the real GUI
	// domain even when another checkout runs this opt-in test concurrently.
	id := saveTestTunnelSpec(t, m, "launchd-fixture-"+home)
	t.Cleanup(func() {
		if err := m.Uninstall(id); err != nil {
			t.Errorf("clean up real LaunchAgent: %v", err)
		}
	})
	binary := filepath.Join(home, "cc-clip-fixture")
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do /bin/sleep 0.1; done\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	waitJob := func(timeout time.Duration, predicate func(TunnelJob) bool) TunnelJob {
		t.Helper()
		deadline := time.Now().Add(timeout)
		var job TunnelJob
		for time.Now().Before(deadline) {
			job, err = m.Inspect(id)
			if err != nil {
				t.Fatal(err)
			}
			if predicate(job) {
				return job
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for real LaunchAgent state: %+v", job)
		return job
	}
	if err := m.Install(binary, id); err != nil {
		t.Fatal(err)
	}
	first := waitJob(15*time.Second, func(job TunnelJob) bool { return job.Process == "running" && job.PID > 0 })
	if err := m.Install(binary, id); err != nil {
		t.Fatalf("repeated install: %v", err)
	}
	if job, err := m.Inspect(id); err != nil || job.PID != first.PID {
		t.Fatalf("repeated install replaced the running job: %+v %v", job, err)
	}
	// Verify the PID belongs to this fixture before injecting an unexpected
	// exit. The manager observes the replacement through production inspection.
	command, err := exec.Command("/bin/ps", "-p", strconv.Itoa(first.PID), "-o", "command=").Output()
	if err != nil || !strings.Contains(string(command), binary) {
		t.Fatalf("cannot verify fixture process ownership: %v", err)
	}
	if err := syscall.Kill(first.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	recovered := waitJob(80*time.Second, func(job TunnelJob) bool {
		return job.Process == "running" && job.PID > 0 && job.PID != first.PID
	})
	t.Log("real launchd recovered an unexpected fixture exit with a new PID")
	if err := m.Stop(id); err != nil {
		t.Fatal(err)
	}
	waitJob(15*time.Second, func(job TunnelJob) bool { return !job.Loaded })
	if err := m.Install(binary, id); err != nil {
		t.Fatalf("restart registration: %v", err)
	}
	waitJob(15*time.Second, func(job TunnelJob) bool {
		return job.Process == "running" && job.PID > 0 && job.PID != recovered.PID
	})
	if err := m.Uninstall(id); err != nil {
		t.Fatal(err)
	}
	waitJob(15*time.Second, func(job TunnelJob) bool { return !job.Loaded })
	if _, err := os.Stat(m.plistPath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture plist remains after uninstall: %v", err)
	}
	for _, path := range []string{m.logPath(id), filepath.Join(home, ".cache", "cc-clip", "tunnel-specs", id+".json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("diagnostic artifact was not retained: %v", err)
		}
	}
}
