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

	// On Windows, use taskkill for graceful termination
	exec.Command("taskkill", "/PID", strconv.Itoa(pid)).Run()
	time.Sleep(500 * time.Millisecond)

	// Force kill if still alive
	if proc.Signal(os.Kill) == nil {
		proc.Kill()
	}

	os.Remove(pidFile)
	fmt.Println("      stopped")
}

func localProcessCommand(pid int) (string, error) {
	out, err := exec.Command("wmic", "process", "where",
		fmt.Sprintf("ProcessId=%d", pid), "get", "CommandLine", "/format:list").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("wmic failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CommandLine=") {
			cmdline := strings.TrimPrefix(line, "CommandLine=")
			if cmdline != "" {
				return cmdline, nil
			}
		}
	}
	return "", fmt.Errorf("process command line not found")
}
