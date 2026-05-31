package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestCLIMutex covers spec scenarios 22-style flag-parse rejections.
// Both `connect` and `setup` must fail-fast at the validation gate
// before any SSH activity — verified by stderr containing the mutex
// message rather than an SSH connection failure for the fake host.
func TestCLIMutex(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "connect_auto_recover_with_token_only",
			args:    []string{"connect", "fakehost.invalid", "--auto-recover", "--token-only"},
			wantErr: "--auto-recover cannot be combined with --token-only",
		},
		{
			name:    "setup_auto_recover_with_token_only",
			args:    []string{"setup", "fakehost.invalid", "--auto-recover", "--token-only"},
			wantErr: "--auto-recover cannot be combined with --token-only",
		},
		{
			name:    "connect_no_hooks_with_token_only",
			args:    []string{"connect", "fakehost.invalid", "--token-only", "--no-hooks"},
			wantErr: "--no-hooks/--hooks cannot be combined with --token-only",
		},
		{
			name:    "connect_hooks_with_token_only",
			args:    []string{"connect", "fakehost.invalid", "--token-only", "--hooks"},
			wantErr: "--no-hooks/--hooks cannot be combined with --token-only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"run", "."}, tc.args...)
			cmd := exec.Command("go", args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatal("expected non-zero exit on flag conflict")
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Fatalf("missing mutex error in stderr: %s", stderr.String())
			}
		})
	}
}
