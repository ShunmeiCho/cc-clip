package shim

import (
	"strings"
	"testing"
)

func TestClaudeWrapperContainsHookInjection(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	for _, needle := range []string{
		"--settings",
		`"Stop"`,
		`"Notification"`,
		"cc-clip-hook",
		"CC_CLIP_PORT:-18339",
		"exec \"$_REAL_CLAUDE\"",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("expected wrapper to contain %q", needle)
		}
	}
}

func TestClaudeWrapperPortSubstitution(t *testing.T) {
	got := ClaudeWrapperScript(9999)
	if !strings.Contains(got, "CC_CLIP_PORT:-9999") {
		t.Error("expected port 9999 in health check URL")
	}
}

func TestClaudeWrapperSkipsOwnDirectory(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	if !strings.Contains(got, `[ "$_dir" = "$_SELF_DIR" ] && continue`) {
		t.Error("expected wrapper to skip its own directory when searching for real claude")
	}
}

func TestClaudeWrapperFallsBackWhenTunnelDown(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	if !strings.Contains(got, "# Tunnel not available") {
		t.Error("expected fallback comment for tunnel-down case")
	}
	// The else branch should exec without --settings
	lines := strings.Split(got, "\n")
	foundElseExec := false
	for _, line := range lines {
		if strings.Contains(line, `exec "$_REAL_CLAUDE" "$@"`) &&
			!strings.Contains(line, "--settings") {
			foundElseExec = true
			break
		}
	}
	if !foundElseExec {
		t.Error("expected fallback exec without --settings flag")
	}
}

func TestClaudeWrapperHasShebangAndHeader(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	if !strings.HasPrefix(got, "#!/usr/bin/env bash") {
		t.Error("expected bash shebang")
	}
	if !strings.Contains(got, "cc-clip claude wrapper") {
		t.Error("expected header comment")
	}
}

func TestClaudeWrapperScript_PrefersSidecar(t *testing.T) {
	script := ClaudeWrapperScript(18339)
	if !strings.Contains(script, "claude.cc-clip-real") {
		t.Fatal("wrapper does not reference sidecar path")
	}
	// Sidecar branch must precede PATH-discovery fallback.
	sidecarIdx := strings.Index(script, "claude.cc-clip-real")
	pathDiscoveryIdx := strings.Index(script, "_PATH_DIRS")
	if sidecarIdx == -1 || pathDiscoveryIdx == -1 {
		t.Fatal("wrapper missing one of: sidecar branch, PATH-discovery fallback")
	}
	if sidecarIdx >= pathDiscoveryIdx {
		t.Fatal("sidecar branch must precede PATH-discovery fallback")
	}
}

func TestClaudeWrapperScript_KeepsPathFallback(t *testing.T) {
	// PATH-discovery must remain for backward compat with legacy installs
	// that have no sidecar.
	script := ClaudeWrapperScript(18339)
	if !strings.Contains(script, "_PATH_DIRS") || !strings.Contains(script, "_SELF_DIR") {
		t.Fatal("wrapper missing PATH-discovery fallback")
	}
}
