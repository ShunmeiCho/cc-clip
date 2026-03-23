//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// stopLocalProcess reads a PID file, verifies the process command, and stops it.
func stopLocalProcess(pidFile string, expectedCmd string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Println("      not running (no PID file)")
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Println("      invalid PID file, removing")
		os.Remove(pidFile)
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("      process not found")
		os.Remove(pidFile)
		return
	}

	if expectedCmd != "" {
		cmdline, err := localProcessCommand(pid)
		if err != nil {
			fmt.Printf("      could not verify command, skipping stop: %v\n", err)
			os.Remove(pidFile)
			return
		}
		if !strings.Contains(strings.ToLower(cmdline), strings.ToLower(expectedCmd)) {
			fmt.Printf("      PID %d belongs to %q, not %q; leaving it running\n", pid, cmdline, expectedCmd)
			os.Remove(pidFile)
			return
		}
	}

	// On Windows, use taskkill for graceful termination, then force kill if needed.
	exec.Command("taskkill", "/PID", strconv.Itoa(pid)).Run()
	time.Sleep(500 * time.Millisecond)
	_ = proc.Kill() // Force kill if still alive (no-op if already exited)

	os.Remove(pidFile)
	fmt.Println("      stopped")
}

// localProcessCommandFunc is the backing implementation for localProcessCommand.
// Overridable for testing.
var localProcessCommandFunc = localProcessCommandImpl

func localProcessCommand(pid int) (string, error) {
	return localProcessCommandFunc(pid)
}

func localProcessCommandImpl(pid int) (string, error) {
	// Use PowerShell Get-CimInstance instead of wmic, which is deprecated
	// and removed by default on Windows 11 24H2+.
	psCmd := fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter "ProcessId=%d" -ErrorAction SilentlyContinue).CommandLine`, pid)
	out, err := exec.Command("powershell", "-NoProfile", "-Command", psCmd).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Get-CimInstance failed: %w", err)
	}
	cmdline := strings.TrimSpace(string(out))
	if cmdline == "" {
		return "", fmt.Errorf("process command line not found")
	}
	return cmdline, nil
}
