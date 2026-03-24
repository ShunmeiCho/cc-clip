//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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

	// Verify process command
	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("      process not found")
		os.Remove(pidFile)
		return
	}

	// Check if alive
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Println("      not running")
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

	// Send SIGTERM
	proc.Signal(syscall.SIGTERM)
	time.Sleep(500 * time.Millisecond)

	// Check if still alive, force kill
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		proc.Signal(syscall.SIGKILL)
	}

	os.Remove(pidFile)
	fmt.Println("      stopped")
}

func localProcessCommand(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ps failed: %w", err)
	}
	cmdline := strings.TrimSpace(string(out))
	if cmdline == "" {
		return "", fmt.Errorf("process command line is empty")
	}
	return cmdline, nil
}
