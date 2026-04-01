package shim

import (
	"strings"
	"testing"
)

func TestHookTemplateUsesNotificationNonceAndHealthLog(t *testing.T) {
	got := HookScript(18339)
	for _, needle := range []string{
		"notify.nonce",
		"notify-health.log",
		"application/x-claude-hook",
		"Authorization: Bearer $_nonce",
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
