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
	if strings.Contains(got, "%!(EXTRA") {
		t.Fatal("wrapper should ignore the legacy port argument without fmt artifacts")
	}
}

func TestClaudeWrapperSkipsOwnDirectory(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	if !strings.Contains(got, `[ "$_dir" = "$_SELF_DIR" ] && continue`) {
		t.Error("expected wrapper to skip its own directory when searching for real claude")
	}
}

func TestClaudeWrapperDoesNotGateInjectionOnStartupHealth(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	if strings.Contains(got, "/health") || strings.Contains(got, "curl ") {
		t.Fatal("wrapper must not skip hook injection based on a startup tunnel probe")
	}
	if !strings.Contains(got, `exec "$_REAL_CLAUDE" --settings`) {
		t.Fatal("wrapper should inject hooks unconditionally when no opt-out marker is present")
	}
}

func TestClaudeWrapperHonorsNoHooksMarker(t *testing.T) {
	got := ClaudeWrapperScript(18339)
	if !strings.Contains(got, `.cache/cc-clip/no-hooks`) {
		t.Fatal("wrapper should check persistent no-hooks marker")
	}
	markerIdx := strings.Index(got, `_NO_HOOKS_FILE`)
	settingsIdx := strings.Index(got, `--settings`)
	if markerIdx == -1 || settingsIdx == -1 || markerIdx >= settingsIdx {
		t.Fatal("no-hooks marker check must run before --settings injection")
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
