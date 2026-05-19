package shim

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeWrapperContainsHookInjection(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	for _, needle := range []string{
		"--settings",
		`"Stop"`,
		`"Notification"`,
		`"matcher": ""`,
		"cc-clip-hook",
		"CC_CLIP_PORT:-18339",
		"exec \"$_REAL_CLAUDE\"",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("expected wrapper to contain %q", needle)
		}
	}
}

func TestClaudeWrapperUsesClaudeV2HookSchema(t *testing.T) {
	script := ClaudeWrapperScript(18339)
	const marker = "--settings '"
	start := strings.Index(script, marker)
	if start == -1 {
		t.Fatal("wrapper missing --settings JSON")
	}
	start += len(marker)
	end := strings.Index(script[start:], "' \"$@\"")
	if end == -1 {
		t.Fatal("wrapper settings JSON terminator not found")
	}
	assertClaudeV2HookSchema(t, script[start:start+end])
}

type claudeHookSettings struct {
	Hooks map[string][]claudeHookMatcher `json:"hooks"`
}

type claudeHookMatcher struct {
	Matcher string              `json:"matcher"`
	Hooks   []claudeHookCommand `json:"hooks"`
}

type claudeHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func assertClaudeV2HookSchema(t *testing.T, cfg string) {
	t.Helper()

	var settings claudeHookSettings
	if err := json.Unmarshal([]byte(cfg), &settings); err != nil {
		t.Fatalf("hook config is not valid JSON: %v\n%s", err, cfg)
	}
	for _, event := range []string{"Notification", "Stop"} {
		matchers := settings.Hooks[event]
		if len(matchers) != 1 {
			t.Fatalf("%s: expected exactly one matcher entry, got %#v", event, matchers)
		}
		if matchers[0].Matcher != "" {
			t.Fatalf("%s: expected empty matcher, got %q", event, matchers[0].Matcher)
		}
		if len(matchers[0].Hooks) != 1 {
			t.Fatalf("%s: expected exactly one hook command, got %#v", event, matchers[0].Hooks)
		}
		cmd := matchers[0].Hooks[0]
		if cmd.Type != "command" || cmd.Command != "cc-clip-hook" {
			t.Fatalf("%s: unexpected hook command: %#v", event, cmd)
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
