package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookTemplateUsesNotificationNonceAndHealthLog(t *testing.T) {
	got := HookScript(18339)
	for _, needle := range []string{
		"notify.nonce",
		"notify-health.log",
		"application/x-claude-hook",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected template to contain %q", needle)
		}
	}
}

func TestHookScriptSubstitutesPort(t *testing.T) {
	got := HookScript(19999)
	if !strings.Contains(got, "19999") {
		t.Fatal("expected template to contain the substituted port 19999")
	}
	// The default port line should have the substituted value, not a format directive.
	// Note: %d also appears in date formats (%Y-%m-%dT) which is expected.
	if strings.Contains(got, "${CC_CLIP_PORT:-%"+"d}") {
		t.Fatal("template still contains unsubstituted port format directive")
	}
}

func TestHookScriptDoesNotUseSessionToken(t *testing.T) {
	got := HookScript(18339)
	if strings.Contains(got, "session.token") {
		t.Fatal("hook template must use notify.nonce, not session.token")
	}
}

func TestHookScriptKeepsNonceOutOfCurlArgv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmpDir := t.TempDir()
	argvLog, stdinLog := writeFakeCurl(t, tmpDir)
	home := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".cache", "cc-clip"), 0700); err != nil {
		t.Fatalf("mkdir home cache: %v", err)
	}
	nonce := "notify-nonce-must-not-appear-in-argv"
	if err := os.WriteFile(filepath.Join(home, ".cache", "cc-clip", "notify.nonce"), []byte(nonce+"\n"), 0600); err != nil {
		t.Fatalf("write nonce: %v", err)
	}

	hookPath := filepath.Join(tmpDir, "cc-clip-hook")
	if err := os.WriteFile(hookPath, []byte(HookScript(18339)), 0755); err != nil {
		t.Fatalf("write hook script: %v", err)
	}

	cmd := exec.Command("bash", hookPath)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop"}`)
	cmd.Env = append(os.Environ(),
		"PATH="+tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"CC_CLIP_HOST_ALIAS=testhost",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook execution failed: %v output=%q", err, string(out))
	}

	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read fake curl argv log: %v", err)
	}
	if strings.Contains(string(argv), nonce) {
		t.Fatalf("nonce leaked through curl argv: %q", string(argv))
	}

	stdinConfig, err := os.ReadFile(stdinLog)
	if err != nil {
		t.Fatalf("read fake curl stdin config: %v", err)
	}
	if !strings.Contains(string(stdinConfig), nonce) {
		t.Fatalf("expected nonce in curl stdin config, got %q", string(stdinConfig))
	}
}

func TestHookScriptAlwaysExitsZero(t *testing.T) {
	got := HookScript(18339)
	if !strings.Contains(got, "exit 0") {
		t.Fatal("hook script must always exit 0 to avoid blocking Claude Code")
	}
}

func TestHookScriptIsValidBash(t *testing.T) {
	got := HookScript(18339)
	if !strings.HasPrefix(got, "#!/usr/bin/env bash") {
		t.Fatal("hook script must start with bash shebang")
	}
	if !strings.Contains(got, "set -euo pipefail") {
		t.Fatal("hook script must use strict mode")
	}
}
