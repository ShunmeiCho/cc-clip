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

// TestCLITargetMatrix covers the Step 5.3b target-resolution flag matrix:
// multi-target conflicts and Claude-scoped hook control must fail-fast at
// parse/validation time (exit 2) BEFORE any SSH activity, proven by the
// target/hook message appearing in stderr instead of an SSH failure for the
// fake host. Mirrors TestCLIMutex's subprocess pattern.
func TestCLITargetMatrix(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "connect_codex_all_conflict",
			args:    []string{"connect", "fakehost.invalid", "--codex", "--all"},
			wantErr: "only one deployment target",
		},
		{
			name:    "setup_codex_all_conflict",
			args:    []string{"setup", "fakehost.invalid", "--codex", "--all"},
			wantErr: "only one deployment target",
		},
		{
			name:    "connect_claude_codex_conflict",
			args:    []string{"connect", "fakehost.invalid", "--claude", "--codex"},
			wantErr: "only one deployment target",
		},
		{
			name:    "connect_codex_no_hooks_rejected",
			args:    []string{"connect", "fakehost.invalid", "--codex", "--no-hooks"},
			wantErr: "--no-hooks/--hooks",
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
				t.Fatal("expected non-zero exit on target/flag conflict")
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Fatalf("missing target-matrix error in stderr: %s", stderr.String())
			}
		})
	}
}
