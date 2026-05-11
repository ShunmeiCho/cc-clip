package shim

import (
	"encoding/json"
	"regexp"
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

// TestClaudeWrapperEmitsValidHookSchema is a regression test for the malformed
// --settings JSON shape that tripped Claude Code 2.x /doctor validation: each
// event entry MUST have an inner "hooks" array, not a flat {type,command}.
// See https://github.com/ShunmeiCho/cc-clip/issues/58.
func TestClaudeWrapperEmitsValidHookSchema(t *testing.T) {
	script := ClaudeWrapperScript(18339)

	// Extract the JSON payload passed to --settings (single-quoted in bash,
	// and the JSON itself contains no single quotes).
	re := regexp.MustCompile(`--settings '(\{[^']*\})'`)
	m := re.FindStringSubmatch(script)
	if len(m) != 2 {
		t.Fatalf("could not extract --settings JSON from wrapper:\n%s", script)
	}

	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher,omitempty"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(m[1]), &parsed); err != nil {
		t.Fatalf("--settings JSON does not parse: %v\npayload: %s", err, m[1])
	}

	// Every Stop / Notification entry must wrap its commands in {hooks: [...]}.
	// A flat entry (the pre-fix shape) parses into a zero-length Hooks slice
	// here and is what Claude Code's /doctor flags as
	//   "hooks.<Event>.0.hooks: Expected array, but received undefined".
	for _, event := range []string{"Stop", "Notification"} {
		entries, ok := parsed.Hooks[event]
		if !ok || len(entries) == 0 {
			t.Errorf("expected at least one %s entry; got %v", event, entries)
			continue
		}
		for i, entry := range entries {
			if len(entry.Hooks) == 0 {
				t.Errorf("%s[%d].hooks is empty; entry must wrap commands in {hooks: [...]}", event, i)
			}
			for j, cmd := range entry.Hooks {
				if cmd.Type != "command" {
					t.Errorf("%s[%d].hooks[%d].type = %q, want %q", event, i, j, cmd.Type, "command")
				}
				if cmd.Command == "" {
					t.Errorf("%s[%d].hooks[%d].command is empty", event, i, j)
				}
			}
		}
	}
}
