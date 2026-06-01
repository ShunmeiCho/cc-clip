package shim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const claudeManagedHookCommand = "env CC_CLIP_MANAGED=1 cc-clip-hook"

var claudeManagedEvents = []string{"Stop", "Notification"}

const (
	claudeSettingsProbeBegin = "__CC_CLIP_CLAUDE_SETTINGS_BEGIN__"
	claudeSettingsProbeEnd   = "__CC_CLIP_CLAUDE_SETTINGS_END__"
)

// MergeRemoteClaudeSettingsHooks installs the cc-clip Claude Code hooks in
// ~/.claude/settings.json. It is idempotent and skips insertion when the user
// already has any cc-clip-hook command configured for Stop/Notification.
func MergeRemoteClaudeSettingsHooks(session SessionExecutor) (bool, error) {
	existing, err := readRemoteClaudeSettings(session)
	if err != nil {
		return false, err
	}
	merged, changed, err := mergeClaudeHooks(existing)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := writeRemoteClaudeSettings(session, merged); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveRemoteClaudeManagedHooks removes only cc-clip's managed hook command
// from ~/.claude/settings.json. User-authored cc-clip-hook entries are left
// intact because cc-clip did not create them.
func RemoveRemoteClaudeManagedHooks(session SessionExecutor) (bool, error) {
	existing, err := readRemoteClaudeSettings(session)
	if err != nil {
		return false, err
	}
	cleaned, changed, err := removeClaudeManagedHooks(existing)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := writeRemoteClaudeSettings(session, cleaned); err != nil {
		return false, err
	}
	return true, nil
}

// RemoteClaudeHooksDisabled reports whether the persistent no-hooks marker is
// present. The marker remains authoritative after settings-first installation
// so users who opted out under the wrapper implementation are not re-enabled by
// a later connect.
func RemoteClaudeHooksDisabled(session RemoteExecutor) (bool, error) {
	out, err := session.Exec(`if [ -f "$HOME/.cache/cc-clip/no-hooks" ]; then echo yes; else echo no; fi`)
	if err != nil {
		return false, fmt.Errorf("failed to check remote claude hook marker: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected claude hook marker probe output: %q", out)
	}
}

func readRemoteClaudeSettings(session RemoteExecutor) ([]byte, error) {
	out, err := session.Exec(fmt.Sprintf(`printf '%%s\n' '%[1]s'
if [ -f "$HOME/.claude/settings.json" ]; then
  cat "$HOME/.claude/settings.json"
fi
printf '\n%%s\n' '%[2]s'`, claudeSettingsProbeBegin, claudeSettingsProbeEnd))
	if err != nil {
		return nil, fmt.Errorf("failed to read remote Claude settings: %w", err)
	}
	data, err := parseRemoteClaudeSettingsProbe(out)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func parseRemoteClaudeSettingsProbe(out string) ([]byte, error) {
	begin := strings.Index(out, claudeSettingsProbeBegin)
	end := strings.LastIndex(out, claudeSettingsProbeEnd)
	if begin < 0 || end < 0 || end < begin {
		return nil, fmt.Errorf("remote Claude settings probe output missing sentinel markers")
	}
	data := out[begin+len(claudeSettingsProbeBegin) : end]
	data = strings.TrimPrefix(data, "\r\n")
	data = strings.TrimPrefix(data, "\n")
	data = strings.TrimSuffix(data, "\r\n")
	data = strings.TrimSuffix(data, "\n")
	return []byte(data), nil
}

func writeRemoteClaudeSettings(session SessionExecutor, data []byte) error {
	cmd := `set -e
mkdir -p "$HOME/.claude"
settings="$HOME/.claude/settings.json"
tmp=$(mktemp "$HOME/.claude/.settings.json.cc-clip.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
if [ -e "$settings" ]; then
  chmod --reference="$settings" "$tmp" 2>/dev/null || chmod 0600 "$tmp"
else
  chmod 0600 "$tmp"
fi
mv "$tmp" "$settings"
trap - EXIT`
	out, err := session.ExecWithStdin(cmd, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to write remote Claude settings: %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

func mergeClaudeHooks(existing []byte) ([]byte, bool, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		existing = []byte(`{}`)
	}

	settings, err := decodeClaudeSettings(existing)
	if err != nil {
		return nil, false, err
	}

	hooks, err := ensureClaudeHooksObject(settings)
	if err != nil {
		return nil, false, err
	}

	changed := false
	for _, event := range claudeManagedEvents {
		eventHooks, err := claudeHookMatchers(hooks, event)
		if err != nil {
			return nil, false, err
		}
		hasCcClipHook, err := claudeEventHasCcClipHook(event, eventHooks)
		if err != nil {
			return nil, false, err
		}
		if hasCcClipHook {
			continue
		}
		hooks[event] = append(eventHooks, map[string]any{
			"matcher": "",
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": claudeManagedHookCommand,
				},
			},
		})
		changed = true
	}

	if !changed {
		return existing, false, nil
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("marshal Claude settings: %w", err)
	}
	return append(out, '\n'), true, nil
}

func removeClaudeManagedHooks(existing []byte) ([]byte, bool, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return existing, false, nil
	}

	settings, err := decodeClaudeSettings(existing)
	if err != nil {
		return nil, false, err
	}

	rawHooks, ok := settings["hooks"]
	if !ok {
		return existing, false, nil
	}
	hooks, ok := rawHooks.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("claude settings hooks must be an object")
	}

	changed := false
	for event, rawEventHooks := range hooks {
		eventHooks, ok := rawEventHooks.([]any)
		if !ok {
			return nil, false, fmt.Errorf("claude settings hooks.%s must be an array", event)
		}

		nextEventHooks := make([]any, 0, len(eventHooks))
		for _, rawMatcher := range eventHooks {
			matcher, ok := rawMatcher.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("claude settings hooks.%s entries must be objects", event)
			}

			rawCommands, ok := matcher["hooks"]
			if !ok {
				nextEventHooks = append(nextEventHooks, rawMatcher)
				continue
			}
			commands, ok := rawCommands.([]any)
			if !ok {
				return nil, false, fmt.Errorf("claude settings hooks.%s entry hooks must be an array", event)
			}

			nextCommands := make([]any, 0, len(commands))
			removed := false
			for _, rawCommand := range commands {
				command, ok := rawCommand.(map[string]any)
				if !ok {
					return nil, false, fmt.Errorf("claude settings hooks.%s command entries must be objects", event)
				}
				if commandString(command) == claudeManagedHookCommand {
					removed = true
					changed = true
					continue
				}
				nextCommands = append(nextCommands, rawCommand)
			}

			if !removed {
				nextEventHooks = append(nextEventHooks, rawMatcher)
				continue
			}
			if len(nextCommands) == 0 && isPlainManagedMatcher(matcher, commands) {
				continue
			}
			matcher["hooks"] = nextCommands
			nextEventHooks = append(nextEventHooks, matcher)
		}

		if len(nextEventHooks) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = nextEventHooks
		}
	}

	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	if !changed {
		return existing, false, nil
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("marshal Claude settings: %w", err)
	}
	return append(out, '\n'), true, nil
}

func decodeClaudeSettings(data []byte) (map[string]any, error) {
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("claude settings JSON is malformed: %w", err)
	}
	if settings == nil {
		settings = make(map[string]any)
	}
	return settings, nil
}

func ensureClaudeHooksObject(settings map[string]any) (map[string]any, error) {
	rawHooks, ok := settings["hooks"]
	if !ok {
		hooks := make(map[string]any)
		settings["hooks"] = hooks
		return hooks, nil
	}
	hooks, ok := rawHooks.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("claude settings hooks must be an object")
	}
	return hooks, nil
}

func claudeHookMatchers(hooks map[string]any, event string) ([]any, error) {
	rawEventHooks, ok := hooks[event]
	if !ok {
		return nil, nil
	}
	eventHooks, ok := rawEventHooks.([]any)
	if !ok {
		return nil, fmt.Errorf("claude settings hooks.%s must be an array", event)
	}
	return eventHooks, nil
}

func claudeEventHasCcClipHook(event string, eventHooks []any) (bool, error) {
	for _, rawMatcher := range eventHooks {
		matcher, ok := rawMatcher.(map[string]any)
		if !ok {
			return false, fmt.Errorf("claude settings hooks.%s entries must be objects", event)
		}
		rawCommands, ok := matcher["hooks"]
		if !ok {
			continue
		}
		commands, ok := rawCommands.([]any)
		if !ok {
			return false, fmt.Errorf("claude settings hooks.%s entry hooks must be an array", event)
		}
		for _, rawCommand := range commands {
			command, ok := rawCommand.(map[string]any)
			if !ok {
				return false, fmt.Errorf("claude settings hooks.%s command entries must be objects", event)
			}
			if strings.Contains(commandString(command), "cc-clip-hook") {
				return true, nil
			}
		}
	}
	return false, nil
}

func commandString(command map[string]any) string {
	cmd, _ := command["command"].(string)
	return cmd
}

func isPlainManagedMatcher(matcher map[string]any, commands []any) bool {
	if len(matcher) != 2 || len(commands) != 1 {
		return false
	}
	matcherValue, _ := matcher["matcher"].(string)
	return matcherValue == ""
}
