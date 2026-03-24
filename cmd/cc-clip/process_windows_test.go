//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
)

func TestLocalProcessCommandReturnsCurrentProcess(t *testing.T) {
	// Verify Get-CimInstance can retrieve the command line for our own process.
	cmdline, err := localProcessCommandImpl(os.Getpid())
	if err != nil {
		t.Fatalf("localProcessCommandImpl(self): %v", err)
	}
	if cmdline == "" {
		t.Fatal("expected non-empty command line for current process")
	}
	// The test binary contains "test" or ".exe" in its command line.
	lower := strings.ToLower(cmdline)
	if !strings.Contains(lower, ".test") && !strings.Contains(lower, ".exe") {
		t.Fatalf("unexpected command line for test process: %s", cmdline)
	}
}

func TestLocalProcessCommandInvalidPID(t *testing.T) {
	// PID 0 or an impossible PID should return an error.
	_, err := localProcessCommandImpl(99999999)
	if err == nil {
		t.Fatal("expected error for non-existent PID")
	}
}
