package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRejectNonClaudeHookControl verifies design §6(b): --no-hooks/--hooks are
// Claude-scoped. They govern the Claude Code settings.json managed hooks only,
// so they are allowed when Claude is a selected target (including under --all,
// where Claude is one of the selected members) and rejected for any target set
// that does not include Claude (codex/opencode/agy alone).
func TestRejectNonClaudeHookControl(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		targets DeployTargets
		noHooks bool
		hooks   bool
		wantErr bool
	}{
		{"claude+no-hooks ok", DeployTargets{Claude: true}, true, false, false},
		{"claude+hooks ok", DeployTargets{Claude: true}, false, true, false},
		{"all+no-hooks ok (claude is a member)", DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}, true, false, false},
		{"codex+no-hooks ERROR", DeployTargets{Codex: true}, true, false, true},
		{"opencode+hooks ERROR", DeployTargets{Opencode: true}, false, true, true},
		{"agy+no-hooks ERROR", DeployTargets{Antigravity: true}, true, false, true},
		{"codex no hook flags ok", DeployTargets{Codex: true}, false, false, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkHookControlTargets(tt.targets, tt.noHooks, tt.hooks)
			if tt.wantErr != (err != nil) {
				t.Fatalf("checkHookControlTargets(%+v, noHooks=%v, hooks=%v) err=%v, wantErr=%v",
					tt.targets, tt.noHooks, tt.hooks, err, tt.wantErr)
			}
		})
	}
}

// TestStdinIsTTYNonTTY verifies the non-*os.File fast path: an in-memory reader
// (as in tests, or a pipe in CI) is never a character device, so an unattended
// run takes the non-TTY fallback instead of blocking on the interactive menu.
func TestStdinIsTTYNonTTY(t *testing.T) {
	t.Parallel()
	if stdinIsTTYReader(bytes.NewReader(nil)) {
		t.Fatal("a non-*os.File reader must not be reported as a TTY")
	}
}

// TestLegacyCodexNotice verifies the one-time breaking notice for the legacy
// single --codex selector: printed (to stderr) for --codex alone, NOT printed
// for --all, and fully suppressed by CC_CLIP_NO_DEPRECATION_NOTICE (which only
// silences the notice; it does not change --codex's new install semantics).
func TestLegacyCodexNotice(t *testing.T) {
	// Not parallel: a subtest uses t.Setenv. Normalize the env up front so the
	// print cases stay deterministic regardless of the ambient environment.
	t.Setenv("CC_CLIP_NO_DEPRECATION_NOTICE", "")

	t.Run("legacy --codex prints one-time breaking notice", func(t *testing.T) {
		var errOut bytes.Buffer
		maybeLegacyCodexNotice(&errOut, []string{"--codex", "myhost"}, DeployTargets{Codex: true})
		if !strings.Contains(errOut.String(), "v0.9.0") || !strings.Contains(errOut.String(), "--all") {
			t.Fatalf("expected one-time breaking notice naming --all:\n%s", errOut.String())
		}
	})

	t.Run("--all does not print legacy notice", func(t *testing.T) {
		var errOut bytes.Buffer
		maybeLegacyCodexNotice(&errOut, []string{"--all"}, DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true})
		if errOut.Len() != 0 {
			t.Fatalf("--all must not print the legacy notice:\n%s", errOut.String())
		}
	})

	t.Run("CC_CLIP_NO_DEPRECATION_NOTICE suppresses the notice", func(t *testing.T) {
		t.Setenv("CC_CLIP_NO_DEPRECATION_NOTICE", "1")
		var errOut bytes.Buffer
		maybeLegacyCodexNotice(&errOut, []string{"--codex", "myhost"}, DeployTargets{Codex: true})
		if errOut.Len() != 0 {
			t.Fatalf("env suppression must silence the notice:\n%s", errOut.String())
		}
	})
}
