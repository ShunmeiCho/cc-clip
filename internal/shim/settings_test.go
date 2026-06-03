package shim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeManagedHookCommandIsRunnerForm pins the inserted command to the
// runner form while keeping the CC_CLIP_MANAGED=1 ownership prefix. The prefix
// is the permanent detection key across the legacy->runner flip.
func TestClaudeManagedHookCommandIsRunnerForm(t *testing.T) {
	const want = "env CC_CLIP_MANAGED=1 cc-clip plugin run claude-notify"
	if claudeManagedHookCommand != want {
		t.Fatalf("claudeManagedHookCommand = %q, want %q", claudeManagedHookCommand, want)
	}
	if !strings.HasPrefix(claudeManagedHookCommand, claudeManagedHookOwnerPrefix) {
		t.Fatalf("managed command %q must carry owner prefix %q", claudeManagedHookCommand, claudeManagedHookOwnerPrefix)
	}
	// The legacy form must still match the owner prefix so it is detected and
	// stripped during forward migration and rollback.
	if !strings.HasPrefix("env CC_CLIP_MANAGED=1 cc-clip-hook", claudeManagedHookOwnerPrefix) {
		t.Fatalf("legacy managed command must match owner prefix %q", claudeManagedHookOwnerPrefix)
	}
	// A bare user cc-clip-hook must NOT match the owner prefix.
	if strings.HasPrefix("cc-clip-hook", claudeManagedHookOwnerPrefix) {
		t.Fatal("bare user cc-clip-hook must not match the owner prefix")
	}
}

func TestMergeClaudeHooksAddsManagedStopAndNotification(t *testing.T) {
	out, changed, warnings, err := mergeClaudeHooks(nil)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("empty settings should be changed")
	}
	if len(warnings) != 0 {
		t.Fatalf("empty settings should produce no warnings, got %v", warnings)
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
	first, changed, _, err := mergeClaudeHooks([]byte(`{}`))
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if !changed {
		t.Fatal("first merge should change settings")
	}

	second, changed, _, err := mergeClaudeHooks(first)
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

// TestMergeClaudeHooks_UserBareHook_SkipsManagedAndWarns asserts the P2#3
// decision: when an event already contains a user-authored bare `cc-clip-hook`
// command (a command whose string CONTAINS "cc-clip-hook" but does NOT carry the
// CC_CLIP_MANAGED=1 ownership prefix), cc-clip must NOT insert its managed runner
// for that event and must NOT strip the user's bare hook. Instead it records a
// per-event warning so the operator can resolve the double-notify risk. Other
// user commands in the event are also left untouched.
func TestMergeClaudeHooks_UserBareHook_SkipsManagedAndWarns(t *testing.T) {
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

	out, changed, warnings, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	// Both managed events have a user bare hook, so nothing is inserted -> no change.
	if changed {
		t.Fatalf("user bare hook in every managed event should leave settings unchanged:\n%s", out)
	}
	text := string(out)
	// Bare user-authored commands must survive verbatim.
	for _, want := range []string{`"cc-clip-hook"`, "custom-stop", "env FOO=1 cc-clip-hook"} {
		if !strings.Contains(text, want) {
			t.Fatalf("user-authored hook %q must be preserved:\n%s", want, text)
		}
	}
	// The managed command must NOT be inserted for any event with a user bare hook.
	if strings.Contains(text, claudeManagedHookCommand) {
		t.Fatalf("managed command must NOT be inserted when a user bare hook is present:\n%s", text)
	}
	// A warning must be recorded for each managed event with a user bare hook.
	if len(warnings) != 2 {
		t.Fatalf("expected one warning per managed event (2 total), got %d: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, "\n")
	for _, event := range claudeManagedEvents {
		if !strings.Contains(joined, event) {
			t.Fatalf("warning should name event %q: %v", event, warnings)
		}
	}
}

// TestMergeClaudeHooks_UserBareHook_OnlyOneEventSkips asserts per-event scoping:
// a user bare hook in ONE managed event suppresses+warns for that event only,
// while the other managed event still receives exactly one current managed
// command.
func TestMergeClaudeHooks_UserBareHook_OnlyOneEventSkips(t *testing.T) {
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "cc-clip-hook"}
        ]
      }
    ]
  }
}`)

	out, changed, warnings, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("Notification has no user bare hook, so the managed command must be inserted there")
	}
	text := string(out)
	// Stop keeps the user bare hook and receives no managed command.
	if !strings.Contains(text, `"cc-clip-hook"`) {
		t.Fatalf("user bare Stop hook must be preserved:\n%s", text)
	}
	// Exactly one managed command total: only the Notification event gets it.
	if got := strings.Count(text, claudeManagedHookCommand); got != 1 {
		t.Fatalf("managed command should be inserted only for Notification (1 total), got %d:\n%s", got, text)
	}
	settings := decodeClaudeSettingsForTest(t, out)
	stopMatchers := settings["hooks"].(map[string]any)["Stop"].([]any)
	for _, rawMatcher := range stopMatchers {
		for _, rawCmd := range rawMatcher.(map[string]any)["hooks"].([]any) {
			if rawCmd.(map[string]any)["command"] == claudeManagedHookCommand {
				t.Fatalf("Stop must not receive the managed command:\n%s", text)
			}
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning for the Stop event, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "Stop") {
		t.Fatalf("warning should name the Stop event: %v", warnings)
	}
}

// TestMergeClaudeHooks_UserBareHook_StripsCoLocatedManaged asserts the P2 fix:
// when an event holds BOTH a user-authored bare cc-clip-hook AND a cc-clip
// owner-prefix managed command (legacy or current), cc-clip defers to the user's
// hook (does NOT insert/keep the managed runner) but STILL strips the
// owner-prefix managed command so the two cannot double-notify. The bare hook
// survives, changed is true (a managed command was removed), and a per-event
// warning is recorded. This is the mixed state the older user-bare tests did not
// cover — their fixtures had no co-located managed command, so they were no-ops.
func TestMergeClaudeHooks_UserBareHook_StripsCoLocatedManaged(t *testing.T) {
	const legacy = "env CC_CLIP_MANAGED=1 cc-clip-hook"
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "cc-clip-hook"},
          {"type": "command", "command": "` + legacy + `"}
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "cc-clip-hook"},
          {"type": "command", "command": "` + claudeManagedHookCommand + `"}
        ]
      }
    ]
  }
}`)

	out, changed, warnings, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("a user-bare hook co-located with a managed command must strip the managed command (changed)")
	}
	text := string(out)
	// Both owner-prefix managed commands (legacy AND current) must be stripped.
	if strings.Contains(text, legacy) {
		t.Fatalf("legacy managed command must be stripped from the user-bare event:\n%s", text)
	}
	if strings.Contains(text, claudeManagedHookCommand) {
		t.Fatalf("managed runner must NOT remain (nor be inserted) when a user bare hook owns the event:\n%s", text)
	}
	// The user's bare hook survives verbatim in both events.
	if got := strings.Count(text, `"cc-clip-hook"`); got != 2 {
		t.Fatalf("both user bare cc-clip-hook commands must be preserved (2), got %d:\n%s", got, text)
	}
	// A warning is recorded for each affected managed event.
	if len(warnings) != 2 {
		t.Fatalf("expected one warning per managed event (2), got %d: %v", len(warnings), warnings)
	}
}

// TestMergeClaudeHooksMigratesLegacyManagedCommand asserts the strip-before-insert
// forward migration: a settings.json carrying the LEGACY managed command
// (env CC_CLIP_MANAGED=1 cc-clip-hook) for Stop/Notification must be rewritten so
// each event ends with EXACTLY ONE managed entry pointing at the NEW command, with
// no legacy managed command left behind and no duplicate.
func TestMergeClaudeHooksMigratesLegacyManagedCommand(t *testing.T) {
	const legacy = "env CC_CLIP_MANAGED=1 cc-clip-hook"
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + legacy + `"}
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + legacy + `"}
        ]
      }
    ]
  }
}`)

	out, changed, _, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("legacy managed command should be migrated to the current command")
	}
	text := string(out)
	if strings.Contains(text, legacy) {
		t.Fatalf("legacy managed command must be stripped, still present:\n%s", text)
	}
	if got := strings.Count(text, claudeManagedHookCommand); got != 2 {
		t.Fatalf("expected exactly one current managed command per event (2 total), got %d:\n%s", got, text)
	}

	settings := decodeClaudeSettingsForTest(t, out)
	for _, event := range []string{"Stop", "Notification"} {
		matchers := settings["hooks"].(map[string]any)[event].([]any)
		if len(matchers) != 1 {
			t.Fatalf("%s should have exactly one matcher after migration, got %d", event, len(matchers))
		}
		hooks := matchers[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("%s should have exactly one command after migration, got %d", event, len(hooks))
		}
		if cmd := hooks[0].(map[string]any)["command"]; cmd != claudeManagedHookCommand {
			t.Fatalf("%s command = %v, want current managed command", event, cmd)
		}
	}
}

// TestMergeClaudeHooksMigrationIsIdempotent asserts that re-running merge on an
// already-migrated remote is a no-op (skip guard sees the current command).
func TestMergeClaudeHooksMigrationIsIdempotent(t *testing.T) {
	const legacy = "env CC_CLIP_MANAGED=1 cc-clip-hook"
	first, changed, _, err := mergeClaudeHooks([]byte(`{
  "hooks": {
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"` + legacy + `"}]}],
    "Notification": [{"matcher":"","hooks":[{"type":"command","command":"` + legacy + `"}]}]
  }
}`))
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if !changed {
		t.Fatal("first merge should migrate")
	}
	second, changed, _, err := mergeClaudeHooks(first)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if changed {
		t.Fatal("second merge on migrated settings should be idempotent")
	}
	if string(first) != string(second) {
		t.Fatalf("idempotent re-merge changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if got := strings.Count(string(second), claudeManagedHookCommand); got != 2 {
		t.Fatalf("still expect one managed command per event after re-merge, got %d", got)
	}
}

// TestMergeClaudeHooksMigrationPreservesSiblingCommands asserts that during
// strip-before-insert, the legacy managed command is removed from a matcher that
// also holds a non-managed command, the non-managed command survives in place,
// and the matcher is NOT collapsed (it is not a plain managed matcher).
func TestMergeClaudeHooksMigrationPreservesSiblingCommands(t *testing.T) {
	const legacy = "env CC_CLIP_MANAGED=1 cc-clip-hook"
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + legacy + `"},
          {"type": "command", "command": "custom-stop"}
        ]
      }
    ]
  }
}`)

	out, changed, _, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("legacy managed command alongside a sibling should still migrate")
	}
	text := string(out)
	if strings.Contains(text, legacy) {
		t.Fatalf("legacy managed command must be stripped:\n%s", text)
	}
	if !strings.Contains(text, "custom-stop") {
		t.Fatalf("sibling non-managed command must be preserved:\n%s", text)
	}

	// Inspect the Stop event specifically: the sibling matcher (with custom-stop)
	// survives, and exactly one current managed command is appended.
	settings := decodeClaudeSettingsForTest(t, out)
	stopMatchers := settings["hooks"].(map[string]any)["Stop"].([]any)
	stopManaged := 0
	siblingPreserved := false
	for _, rawMatcher := range stopMatchers {
		for _, rawCmd := range rawMatcher.(map[string]any)["hooks"].([]any) {
			switch rawCmd.(map[string]any)["command"] {
			case claudeManagedHookCommand:
				stopManaged++
			case "custom-stop":
				siblingPreserved = true
			}
		}
	}
	if !siblingPreserved {
		t.Fatalf("custom-stop sibling must remain in the Stop event:\n%s", text)
	}
	if stopManaged != 1 {
		t.Fatalf("expected exactly one current managed command in Stop, got %d:\n%s", stopManaged, text)
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

	out, changed, _, err := mergeClaudeHooks(existing)
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
	if _, _, _, err := mergeClaudeHooks([]byte(`{"hooks":`)); err == nil {
		t.Fatal("invalid JSON should be rejected")
	}
}

func TestMergeClaudeHooksRejectsMalformedHookSchema(t *testing.T) {
	if _, _, _, err := mergeClaudeHooks([]byte(`{"hooks":{"Stop":["not-an-object"]}}`)); err == nil {
		t.Fatal("malformed hook schema should be rejected instead of rewritten")
	}
}

// TestMergeClaudeHooks_MixedLegacyAndCurrent_StripsLegacyKeepsOneCurrent
// asserts the P1#1 mixed-state fix: an event holding BOTH the legacy managed
// command AND the current managed command must be rewritten so the legacy is
// stripped, leaving EXACTLY ONE current managed command (count==1 per event).
// The pre-strip skip guard would have left the legacy behind; strip-then-insert
// repairs it.
func TestMergeClaudeHooks_MixedLegacyAndCurrent_StripsLegacyKeepsOneCurrent(t *testing.T) {
	const legacy = "env CC_CLIP_MANAGED=1 cc-clip-hook"
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + legacy + `"},
          {"type": "command", "command": "` + claudeManagedHookCommand + `"}
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + claudeManagedHookCommand + `"}
        ]
      },
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + legacy + `"}
        ]
      }
    ]
  }
}`)

	out, changed, warnings, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("a mixed legacy+current event must be rewritten to drop the legacy command")
	}
	if len(warnings) != 0 {
		t.Fatalf("mixed managed state should not warn, got %v", warnings)
	}
	text := string(out)
	if strings.Contains(text, legacy) {
		t.Fatalf("legacy managed command must be stripped from the mixed event:\n%s", text)
	}
	settings := decodeClaudeSettingsForTest(t, out)
	for _, event := range []string{"Stop", "Notification"} {
		matchers := settings["hooks"].(map[string]any)[event].([]any)
		current := 0
		for _, rawMatcher := range matchers {
			for _, rawCmd := range rawMatcher.(map[string]any)["hooks"].([]any) {
				if rawCmd.(map[string]any)["command"] == claudeManagedHookCommand {
					current++
				}
			}
		}
		if current != 1 {
			t.Fatalf("%s must hold exactly one current managed command after merge, got %d:\n%s", event, current, text)
		}
	}
}

// TestMergeClaudeHooks_AlreadyExactlyCurrent_NoChange asserts idempotency: an
// event consisting of EXACTLY one current managed command (and nothing else
// managed) must not be rewritten. changed==false and bytes are returned
// verbatim.
func TestMergeClaudeHooks_AlreadyExactlyCurrent_NoChange(t *testing.T) {
	existing := []byte(`{
  "hooks": {
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"` + claudeManagedHookCommand + `"}]}],
    "Notification": [{"matcher":"","hooks":[{"type":"command","command":"` + claudeManagedHookCommand + `"}]}]
  }
}`)

	out, changed, warnings, err := mergeClaudeHooks(existing)
	if err != nil {
		t.Fatalf("mergeClaudeHooks returned error: %v", err)
	}
	if changed {
		t.Fatalf("already-exactly-current settings must be a no-op:\n%s", out)
	}
	if len(warnings) != 0 {
		t.Fatalf("exact-current settings should not warn, got %v", warnings)
	}
	if string(out) != string(existing) {
		t.Fatalf("no-op merge must return bytes verbatim\nwant:\n%s\ngot:\n%s", existing, out)
	}
}

func TestRemoveClaudeManagedHooksOnlyRemovesManagedCommands(t *testing.T) {
	// Owner-prefix union strip: both the legacy managed command
	// (env CC_CLIP_MANAGED=1 cc-clip-hook) AND the current managed command
	// (env CC_CLIP_MANAGED=1 cc-clip plugin run claude-notify) carry the
	// CC_CLIP_MANAGED=1 ownership prefix and must be removed. A bare
	// user-authored `cc-clip-hook` lacks the prefix and must survive.
	const legacy = "env CC_CLIP_MANAGED=1 cc-clip-hook"
	existing := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + legacy + `"},
          {"type": "command", "command": "cc-clip-hook"},
          {"type": "command", "command": "custom-stop"}
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "` + claudeManagedHookCommand + `"}
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
		t.Fatalf("current managed command still present:\n%s", text)
	}
	if strings.Contains(text, legacy) {
		t.Fatalf("legacy managed command must also be stripped (owner-prefix union):\n%s", text)
	}
	// Bare user hook (no ownership prefix) and unrelated user hook survive.
	if !strings.Contains(text, `"cc-clip-hook"`) {
		t.Fatalf("bare user cc-clip-hook should be preserved:\n%s", text)
	}
	if !strings.Contains(text, "custom-stop") {
		t.Fatalf("user hook custom-stop should be preserved:\n%s", text)
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

	changed, warnings, err := MergeRemoteClaudeSettingsHooks(s)
	if err != nil {
		t.Fatalf("MergeRemoteClaudeSettingsHooks returned error: %v", err)
	}
	if !changed {
		t.Fatal("missing settings should be changed")
	}
	if len(warnings) != 0 {
		t.Fatalf("clean install should produce no warnings, got %v", warnings)
	}

	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	if strings.Count(string(data), claudeManagedHookCommand) != 2 {
		t.Fatalf("settings should contain two managed hooks, got:\n%s", data)
	}

	changed, _, err = MergeRemoteClaudeSettingsHooks(s)
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
		t.Fatalf("current managed hook still present:\n%s", data)
	}
	// The seeded `env CC_CLIP_MANAGED=1 cc-clip-hook` carries the ownership
	// prefix and must be stripped via the owner-prefix union.
	if strings.Contains(string(data), "env CC_CLIP_MANAGED=1 cc-clip-hook") {
		t.Fatalf("legacy managed hook should be stripped (owner-prefix union):\n%s", data)
	}
	// The bare user-authored cc-clip-hook lacks the prefix and must remain.
	if !strings.Contains(string(data), `"cc-clip-hook"`) {
		t.Fatalf("bare user hook should remain:\n%s", data)
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
