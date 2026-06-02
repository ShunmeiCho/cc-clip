package shim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// claudeManagedHookCommand is the command cc-clip INSERTS for managed events.
// Per-release this flips; only the suffix changes. The CC_CLIP_MANAGED=1 prefix
// is the permanent ownership marker.
const claudeManagedHookCommand = "env CC_CLIP_MANAGED=1 cc-clip plugin run claude-notify"

// claudeManagedHookOwnerPrefix matches the UNION of every cc-clip-owned managed
// command across releases: legacy "...cc-clip-hook" AND new "...plugin run
// claude-notify". Detection/strip key off THIS prefix permanently; only the
// INSERT string (claudeManagedHookCommand) flips. This guarantees idempotent
// forward migration and clean binary rollback. A bare user-authored
// "cc-clip-hook" lacks this prefix and is therefore never matched or stripped.
const claudeManagedHookOwnerPrefix = "env CC_CLIP_MANAGED=1"

var claudeManagedEvents = []string{"Stop", "Notification"}

// isManagedClaudeCommand reports whether command is cc-clip-owned, keying off the
// permanent CC_CLIP_MANAGED=1 ownership prefix (the union of legacy and current
// managed commands). User-authored bare cc-clip-hook commands are not matched.
func isManagedClaudeCommand(command map[string]any) bool {
	return strings.HasPrefix(commandString(command), claudeManagedHookOwnerPrefix)
}

const (
	claudeSettingsProbeBegin = "__CC_CLIP_CLAUDE_SETTINGS_BEGIN__"
	claudeSettingsProbeEnd   = "__CC_CLIP_CLAUDE_SETTINGS_END__"
)

// MergeRemoteClaudeSettingsHooks installs the cc-clip Claude Code hooks in
// ~/.claude/settings.json. It strips the owner-prefix union (legacy + current
// managed commands) before inserting exactly one current managed command per
// event, then writes the file in a single atomic rename. It is idempotent: when
// exactly one current managed command is already present for an event, that
// event is left untouched. It returns any per-event warnings (e.g. a detected
// user-authored bare cc-clip-hook the caller should surface) regardless of
// whether the file changed.
func MergeRemoteClaudeSettingsHooks(session SessionExecutor) (bool, []string, error) {
	existing, err := readRemoteClaudeSettings(session)
	if err != nil {
		return false, nil, err
	}
	merged, changed, warnings, err := mergeClaudeHooks(existing)
	if err != nil {
		return false, nil, err
	}
	if !changed {
		return false, warnings, nil
	}
	if err := writeRemoteClaudeSettings(session, merged); err != nil {
		return false, nil, err
	}
	return true, warnings, nil
}

// RemoveRemoteClaudeManagedHooks removes cc-clip's managed hook commands from
// ~/.claude/settings.json, matching the owner-prefix union (legacy + current).
// User-authored bare cc-clip-hook entries lack the CC_CLIP_MANAGED=1 prefix and
// are left intact because cc-clip did not create them.
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

// mergeClaudeHooks installs exactly one current managed command per managed
// event, returning the rewritten settings, whether anything changed, and any
// per-event warnings the caller should surface. Per event it applies:
//
//  1. USER-BARE DETECTION (P2#3): if the event already holds a user-authored
//     bare cc-clip-hook (a command CONTAINING "cc-clip-hook" but WITHOUT the
//     CC_CLIP_MANAGED=1 ownership prefix), cc-clip skips insertion and strips
//     nothing for that event, recording a warning so the operator can resolve
//     the double-notify risk.
//  2. STRIP-THEN-INSERT (P1#1 mixed-state fix): otherwise strip every
//     owner-prefix managed command (legacy + current) from the event. If the
//     event consisted of EXACTLY one managed command and it was already the
//     current command, skip the rewrite (idempotent). Else append exactly one
//     current managed matcher. A MIXED state (current + legacy) is repaired:
//     the legacy is stripped, leaving exactly one current.
func mergeClaudeHooks(existing []byte) ([]byte, bool, []string, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		existing = []byte(`{}`)
	}

	settings, err := decodeClaudeSettings(existing)
	if err != nil {
		return nil, false, nil, err
	}

	hooks, err := ensureClaudeHooksObject(settings)
	if err != nil {
		return nil, false, nil, err
	}

	changed := false
	var warnings []string
	for _, event := range claudeManagedEvents {
		eventHooks, err := claudeHookMatchers(hooks, event)
		if err != nil {
			return nil, false, nil, err
		}

		// P2#3: a user-authored bare cc-clip-hook means the operator already
		// wired their own notification. Do not insert the managed runner and do
		// not strip the user's hook; warn and move on.
		userBare, err := claudeEventHasUserBareCcClipHook(event, eventHooks)
		if err != nil {
			return nil, false, nil, err
		}
		if userBare {
			warnings = append(warnings, fmt.Sprintf("detected a user-authored cc-clip-hook for %s; skipping cc-clip managed hook to avoid double-notify (remove it or keep relying on your own hook)", event))
			continue
		}

		// Count owner-prefix managed commands BEFORE stripping so we can detect
		// the idempotent case (exactly one managed command and it is the current
		// command) versus a mixed/legacy state that needs repair.
		total, current, err := claudeEventManagedCommandCounts(event, eventHooks)
		if err != nil {
			return nil, false, nil, err
		}
		if total == 1 && current == 1 {
			continue // already exactly the current managed command; leave untouched
		}

		// Strip-before-insert: remove every owner-prefix-matching managed command
		// (legacy + any stale/duplicate current) from this event, then append
		// exactly one current managed matcher. The whole file is written in one
		// atomic rename by writeRemoteClaudeSettings, so there is no on-disk
		// instant where both the legacy and current managed commands fire.
		stripped, _, err := stripManagedFromEvent(eventHooks, event)
		if err != nil {
			return nil, false, nil, err
		}
		hooks[event] = append(stripped, map[string]any{
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
		return existing, false, warnings, nil
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, nil, fmt.Errorf("marshal Claude settings: %w", err)
	}
	return append(out, '\n'), true, warnings, nil
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

		nextEventHooks, removed, err := stripManagedFromEvent(eventHooks, event)
		if err != nil {
			return nil, false, err
		}
		if removed {
			changed = true
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

// stripManagedFromEvent removes all owner-prefix-matching managed commands from
// one event's matcher slice, collapsing now-empty plain managed matchers via
// isPlainManagedMatcher. It is the shared core used by both
// removeClaudeManagedHooks and mergeClaudeHooks. Non-managed commands and
// non-plain matchers are preserved verbatim.
func stripManagedFromEvent(eventHooks []any, event string) (next []any, removed bool, err error) {
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
		matcherRemoved := false
		for _, rawCommand := range commands {
			command, ok := rawCommand.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("claude settings hooks.%s command entries must be objects", event)
			}
			if isManagedClaudeCommand(command) {
				matcherRemoved = true
				removed = true
				continue
			}
			nextCommands = append(nextCommands, rawCommand)
		}

		if !matcherRemoved {
			nextEventHooks = append(nextEventHooks, rawMatcher)
			continue
		}
		if len(nextCommands) == 0 && isPlainManagedMatcher(matcher, commands) {
			continue
		}
		matcher["hooks"] = nextCommands
		nextEventHooks = append(nextEventHooks, matcher)
	}
	return nextEventHooks, removed, nil
}

// claudeEventManagedCommandCounts walks one event's matchers and returns the
// total number of owner-prefix managed commands and how many of those equal the
// CURRENT managed command. The merge uses (total==1 && current==1) as the
// idempotent-skip guard: exactly one managed command that is already current.
// Any other shape (legacy present, duplicate current, mixed legacy+current)
// fails that guard and is repaired by strip-then-insert.
func claudeEventManagedCommandCounts(event string, eventHooks []any) (total, current int, err error) {
	for _, rawMatcher := range eventHooks {
		matcher, ok := rawMatcher.(map[string]any)
		if !ok {
			return 0, 0, fmt.Errorf("claude settings hooks.%s entries must be objects", event)
		}
		rawCommands, ok := matcher["hooks"]
		if !ok {
			continue
		}
		commands, ok := rawCommands.([]any)
		if !ok {
			return 0, 0, fmt.Errorf("claude settings hooks.%s entry hooks must be an array", event)
		}
		for _, rawCommand := range commands {
			command, ok := rawCommand.(map[string]any)
			if !ok {
				return 0, 0, fmt.Errorf("claude settings hooks.%s command entries must be objects", event)
			}
			if !isManagedClaudeCommand(command) {
				continue
			}
			total++
			if commandString(command) == claudeManagedHookCommand {
				current++
			}
		}
	}
	return total, current, nil
}

// claudeEventHasUserBareCcClipHook reports whether the event holds a
// user-authored bare cc-clip-hook: a command whose string CONTAINS
// "cc-clip-hook" but does NOT carry the CC_CLIP_MANAGED=1 ownership prefix.
// Such a hook signals the operator already wired their own notification, so the
// merge must skip insertion (P2#3) rather than risk double-notify.
func claudeEventHasUserBareCcClipHook(event string, eventHooks []any) (bool, error) {
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
			cmd := commandString(command)
			if strings.Contains(cmd, "cc-clip-hook") && !strings.HasPrefix(cmd, claudeManagedHookOwnerPrefix) {
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
