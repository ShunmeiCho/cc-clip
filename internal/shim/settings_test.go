package shim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeClaudeHooksAddsManagedStopAndNotification(t *testing.T) {
	out, changed, err := mergeClaudeHooks(nil)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("empty settings should be changed")
	}

	settings := decodeClaudeSettingsForTest(t, out)
	for _, event := range []string{"Stop", "Notification"} {
		matchers := settings["hooks"].(map[string]any)[event].([]any)
		if len(matchers) != 1 {
			t.Fatalf("%s matcher count = %d, want 1", event, len(matchers))
		}
		hooks := matchers[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("%s hook count = %d, want 1", event, len(hooks))
		}
		hook := hooks[0].(map[string]any)
		if hook["type"] != "command" || hook["command"] != claudeManagedHookCommand {
			t.Fatalf("%s hook = %#v, want managed command", event, hook)
		}
	}
}

func TestMergeClaudeHooksIsIdempotentForManagedHooks(t *testing.T) {
	first, changed, err := mergeClaudeHooks([]byte(`{}`))
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if !changed {
		t.Fatal("first merge should change settings")
	}

	second, changed, err := mergeClaudeHooks(first)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if changed {
		t.Fatal("second merge should be idempotent")
	}
	if string(first) != string(second) {
		t.Fatalf("idempotent merge changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeClaudeHooksSkipsExistingUserCcClipHook(t *testing.T) {
	existing := []byte(`{
  "theme": "dark",
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "cc-clip-hook"},
          {"type": "command", "command": "custom-stop"}
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "env FOO=1 cc-clip-hook"}
        ]
      }
    ]
  }
}`)

	out, changed, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if changed {
		t.Fatal("settings with existing user cc-clip-hook commands should not be changed")
	}
	if string(out) != string(existing) {
		t.Fatal("unchanged settings should be returned byte-for-byte")
	}
}

func TestMergeClaudeHooksPreservesOtherHooks(t *testing.T) {
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "git",
        "hooks": [
          {"type": "command", "command": "custom-stop"}
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "custom-pre"}
        ]
      }
    ]
  }
}`)

	out, changed, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("missing managed Notification/Stop hook should change settings")
	}
	text := string(out)
	for _, want := range []string{"custom-stop", "custom-pre", claudeManagedHookCommand} {
		if !strings.Contains(text, want) {
			t.Fatalf("merged settings missing %q:\n%s", want, text)
		}
	}
}

func TestMergeClaudeHooksRejectsInvalidJSON(t *testing.T) {
	if _, _, err := mergeClaudeHooks([]byte(`{"hooks":`)); err == nil {
		t.Fatal("invalid JSON should be rejected")
	}
}

func TestMergeClaudeHooksRejectsMalformedHookSchema(t *testing.T) {
	if _, _, err := mergeClaudeHooks([]byte(`{"hooks":{"Stop":["not-an-object"]}}`)); err == nil {
		t.Fatal("malformed hook schema should be rejected instead of rewritten")
	}
}

func TestRemoveClaudeManagedHooksOnlyRemovesManagedCommands(t *testing.T) {
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "env CC_CLIP_MANAGED=1 cc-clip-hook"},
          {"type": "command", "command": "cc-clip-hook"},
          {"type": "command", "command": "custom-stop"}
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "env CC_CLIP_MANAGED=1 cc-clip-hook"}
        ]
      }
    ]
  }
}`)

	out, changed, err := removeClaudeManagedHooks(existing)
	if err != nil {
		t.Fatalf("removeClaudeManagedHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("managed commands should be removed")
	}
	text := string(out)
	if strings.Contains(text, claudeManagedHookCommand) {
		t.Fatalf("managed command still present:\n%s", text)
	}
	for _, want := range []string{"cc-clip-hook", "custom-stop"} {
		if !strings.Contains(text, want) {
			t.Fatalf("user hook %q should be preserved:\n%s", want, text)
		}
	}
}

func TestRemoveClaudeManagedHooksIsNoopWithoutManagedCommands(t *testing.T) {
	existing := []byte(`{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"cc-clip-hook"}]}]}}`)
	out, changed, err := removeClaudeManagedHooks(existing)
	if err != nil {
		t.Fatalf("removeClaudeManagedHooks returned error: %v", err)
	}
	if changed {
		t.Fatal("user-managed cc-clip-hook should not be removed")
	}
	if string(out) != string(existing) {
		t.Fatal("unchanged settings should be returned byte-for-byte")
	}
}

func TestParseRemoteClaudeSettingsProbeIgnoresOuterNoise(t *testing.T) {
	out := "login banner\n" +
		claudeSettingsProbeBegin + "\n" +
		"{\"hooks\":{}}\n" +
		claudeSettingsProbeEnd + "\n" +
		"logout banner\n"

	data, err := parseRemoteClaudeSettingsProbe(out)
	if err != nil {
		t.Fatalf("parseRemoteClaudeSettingsProbe returned error: %v", err)
	}
	if string(data) != `{"hooks":{}}` {
		t.Fatalf("parsed settings = %q", data)
	}
}

func TestMergeRemoteClaudeSettingsHooksWritesSettings(t *testing.T) {
	s := &localSession{home: t.TempDir()}

	changed, err := MergeRemoteClaudeSettingsHooks(s)
	if err != nil {
		t.Fatalf("MergeRemoteClaudeSettingsHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("missing settings should be changed")
	}

	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	if strings.Count(string(data), claudeManagedHookCommand) != 2 {
		t.Fatalf("settings should contain two managed hooks, got:\n%s", data)
	}

	changed, err = MergeRemoteClaudeSettingsHooks(s)
	if err != nil {
		t.Fatalf("second MergeRemoteClaudeSettingsHooks returned error: %v", err)
	}
	if changed {
		t.Fatal("second merge should be idempotent")
	}
}

func TestRemoveRemoteClaudeManagedHooksPreservesUserHook(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "env CC_CLIP_MANAGED=1 cc-clip-hook"},
          {"type": "command", "command": "cc-clip-hook"}
        ]
      }
    ]
  }
}`), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	changed, err := RemoveRemoteClaudeManagedHooks(s)
	if err != nil {
		t.Fatalf("RemoveRemoteClaudeManagedHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("managed hook should be removed")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(data), claudeManagedHookCommand) {
		t.Fatalf("managed hook still present:\n%s", data)
	}
	if !strings.Contains(string(data), `"cc-clip-hook"`) {
		t.Fatalf("user hook should remain:\n%s", data)
	}
}

func TestRemoteClaudeHooksDisabledReadsMarker(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	disabled, err := RemoteClaudeHooksDisabled(s)
	if err != nil {
		t.Fatalf("RemoteClaudeHooksDisabled returned error: %v", err)
	}
	if disabled {
		t.Fatal("missing marker should not disable hooks")
	}

	marker := filepath.Join(s.home, ".cache", "cc-clip", "no-hooks")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	disabled, err = RemoteClaudeHooksDisabled(s)
	if err != nil {
		t.Fatalf("RemoteClaudeHooksDisabled returned error after marker: %v", err)
	}
	if !disabled {
		t.Fatal("marker should disable hooks")
	}
}

func decodeClaudeSettingsForTest(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return settings
}
