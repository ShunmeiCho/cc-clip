package shim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// cursorManagedHookCommand is the command cc-clip inserts into the remote
// ~/.cursor/hooks.json `stop` event.
//
// The shape is deliberate, and it differs from the Claude one
// (claudeManagedHookCommand), which is a bare `cc-clip ...`:
//
//   - The binary is addressed by ABSOLUTE path. Cursor runs a user hook from
//     ~/.cursor/ "with the system's standard environment", not from an
//     interactive login shell, so ~/.local/bin is not guaranteed to be on PATH
//     — the same hazard this repo already documents for remote command
//     resolution. The reported symptom when it bites is a hook that spawns and
//     exits 127 while Cursor surfaces nothing, which is indistinguishable from
//     a hook that never fired.
//   - The absolute path is produced by `sh -c` expanding $HOME rather than
//     baked in at install time, so the entry stays correct if the remote home
//     is ever remounted at a different path, and so cc-clip does not have to
//     probe for it.
//   - `env` leads, which makes the command work whether or not Cursor passes it
//     through a shell: if it does, sh expands $HOME; if it does not, `env`
//     itself resolves from any standard PATH and then runs sh.
const cursorManagedHookCommand = `env CC_CLIP_MANAGED=1 sh -c 'exec "$HOME/.local/bin/cc-clip" plugin run cursor-notify'`

// cursorManagedHookOwnerPrefix is the permanent ownership marker. Merge and
// strip key off this rather than off the full command so a future change to
// the command's tail does not orphan the entries already deployed.
const cursorManagedHookOwnerPrefix = "env CC_CLIP_MANAGED=1"

// cursorStopEvent is the only Cursor hook event cc-clip registers for. It is
// the end-of-turn signal; `sessionEnd` fires too late to be useful as an
// "your turn" notification.
const cursorStopEvent = "stop"

// cursorHooksSchemaVersion is the version field Cursor's hooks.json requires.
const cursorHooksSchemaVersion = 1

// cursorHookTimeoutSeconds bounds the hook. Posting to a loopback daemon
// through an already-open tunnel is fast; the timeout exists so a wedged
// daemon cannot stall the end of every Cursor turn.
const cursorHookTimeoutSeconds = 10

const (
	cursorHooksProbeBegin = "__CC_CLIP_CURSOR_HOOKS_BEGIN__"
	cursorHooksProbeEnd   = "__CC_CLIP_CURSOR_HOOKS_END__"
)

// RemoteHasCursor reports whether the Cursor CLI is runnable on the remote.
// It probes `command -v cursor-agent` — a present ~/.cursor/ directory does
// not imply the executable is installed. Tri-state parsing mirrors
// RemoteHasOpencode.
func RemoteHasCursor(session RemoteExecutor) (bool, error) {
	out, err := session.Exec("if command -v cursor-agent >/dev/null 2>&1; then echo yes; else echo no; fi")
	if err != nil {
		return false, fmt.Errorf("failed to check for remote Cursor CLI: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected Cursor probe output: %q", out)
	}
}

// MergeRemoteCursorHooks installs the cc-clip stop hook in ~/.cursor/hooks.json.
//
// The file is merged rather than dropped: unlike opencode's plugins directory,
// hooks.json is a single document the user may already own, and overwriting it
// would silently delete their hooks. Everything outside the stop event is
// preserved verbatim, and the stop event keeps every command that is not ours.
//
// Idempotent: when the stop event already holds exactly one cc-clip command and
// it is the current one, nothing is written.
func MergeRemoteCursorHooks(session SessionExecutor) (bool, error) {
	existing, err := readRemoteCursorHooks(session)
	if err != nil {
		return false, err
	}
	merged, changed, err := mergeCursorHooks(existing)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := writeRemoteCursorHooks(session, merged); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveRemoteCursorManagedHooks removes cc-clip's own stop-hook entries from
// ~/.cursor/hooks.json, leaving every user-authored command in place.
//
// Symmetric helper, unit-tested but not yet called from an uninstall branch —
// the same position StripRemoteOpencodePlugin is in.
func RemoveRemoteCursorManagedHooks(session SessionExecutor) (bool, error) {
	existing, err := readRemoteCursorHooks(session)
	if err != nil {
		return false, err
	}
	cleaned, changed, err := removeCursorManagedHooks(existing)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := writeRemoteCursorHooks(session, cleaned); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureRemoteCursorHooks adapts MergeRemoteCursorHooks to the
// detectInstallAdapter install signature. The port is unused: the Cursor hook
// runs `cc-clip plugin run cursor-notify` on the remote, and that subcommand
// resolves the daemon port itself, exactly as the Claude managed hook does.
func EnsureRemoteCursorHooks(session RemoteExecutor, _ int) error {
	sess, ok := session.(SessionExecutor)
	if !ok {
		return fmt.Errorf("cursor hook install needs a session that can write stdin")
	}
	if _, err := MergeRemoteCursorHooks(sess); err != nil {
		return err
	}
	return nil
}

func readRemoteCursorHooks(session RemoteExecutor) ([]byte, error) {
	out, err := session.Exec(fmt.Sprintf(`printf '%%s\n' '%[1]s'
if [ -f "$HOME/.cursor/hooks.json" ]; then
  cat "$HOME/.cursor/hooks.json"
fi
printf '\n%%s\n' '%[2]s'`, cursorHooksProbeBegin, cursorHooksProbeEnd))
	if err != nil {
		return nil, fmt.Errorf("failed to read remote Cursor hooks: %w", err)
	}
	return parseRemoteCursorHooksProbe(out)
}

// parseRemoteCursorHooksProbe extracts the file body from between the
// sentinels. Sentinels are used rather than a bare `cat` because the remote
// shell prelude and any rc-file chatter would otherwise be indistinguishable
// from file content.
func parseRemoteCursorHooksProbe(out string) ([]byte, error) {
	begin := strings.Index(out, cursorHooksProbeBegin)
	end := strings.LastIndex(out, cursorHooksProbeEnd)
	if begin < 0 || end < 0 || end < begin {
		return nil, fmt.Errorf("remote Cursor hooks probe output missing sentinel markers")
	}
	data := out[begin+len(cursorHooksProbeBegin) : end]
	data = strings.TrimPrefix(data, "\r\n")
	data = strings.TrimPrefix(data, "\n")
	data = strings.TrimSuffix(data, "\r\n")
	data = strings.TrimSuffix(data, "\n")
	return []byte(data), nil
}

func writeRemoteCursorHooks(session SessionExecutor, data []byte) error {
	cmd := `set -e
mkdir -p "$HOME/.cursor"
hooks="$HOME/.cursor/hooks.json"
tmp=$(mktemp "$HOME/.cursor/.hooks.json.cc-clip.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
if [ -e "$hooks" ]; then
  chmod --reference="$hooks" "$tmp" 2>/dev/null || chmod 0600 "$tmp"
else
  chmod 0600 "$tmp"
fi
mv "$tmp" "$hooks"
trap - EXIT`
	out, err := session.ExecWithStdin(cmd, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to write remote Cursor hooks: %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

// mergeCursorHooks inserts exactly one current managed command into the stop
// event, returning the rewritten document and whether anything changed.
//
// An unparseable existing file is an error rather than a silent overwrite: the
// user's hooks are not ours to discard because we could not read them.
func mergeCursorHooks(existing []byte) ([]byte, bool, error) {
	doc, err := decodeCursorHooks(existing)
	if err != nil {
		return nil, false, err
	}

	hooks := childObject(doc, "hooks")
	event := commandList(hooks[cursorStopEvent])

	kept, managed := partitionCursorCommands(event)
	if managed == 1 && cursorCommandIsCurrent(event) {
		return nil, false, nil
	}

	hooks[cursorStopEvent] = append(kept, newCursorManagedCommand())
	doc["hooks"] = hooks
	if _, ok := doc["version"]; !ok {
		doc["version"] = cursorHooksSchemaVersion
	}

	out, err := marshalCursorHooks(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// removeCursorManagedHooks drops every cc-clip-owned command from the stop
// event, leaving user-authored entries and the rest of the document untouched.
// An emptied stop event is deleted rather than left as an empty array so the
// file returns to the shape it had before cc-clip touched it.
func removeCursorManagedHooks(existing []byte) ([]byte, bool, error) {
	doc, err := decodeCursorHooks(existing)
	if err != nil {
		return nil, false, err
	}

	hooksRaw, ok := doc["hooks"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	event := commandList(hooksRaw[cursorStopEvent])
	kept, managed := partitionCursorCommands(event)
	if managed == 0 {
		return nil, false, nil
	}

	if len(kept) == 0 {
		delete(hooksRaw, cursorStopEvent)
	} else {
		hooksRaw[cursorStopEvent] = kept
	}
	doc["hooks"] = hooksRaw

	out, err := marshalCursorHooks(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// decodeCursorHooks parses the file body, treating an absent or whitespace-only
// file as an empty document.
func decodeCursorHooks(existing []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(existing, &doc); err != nil {
		return nil, fmt.Errorf("remote ~/.cursor/hooks.json is not valid JSON, refusing to overwrite it: %w", err)
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	return doc, nil
}

func marshalCursorHooks(doc map[string]any) ([]byte, error) {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode Cursor hooks: %w", err)
	}
	return append(out, '\n'), nil
}

// childObject returns doc[key] as an object, replacing a missing or
// wrong-typed value with a fresh one. A wrong-typed value is overwritten
// because `hooks` not being an object means the file is not a hooks.json in
// the first place.
func childObject(doc map[string]any, key string) map[string]any {
	if child, ok := doc[key].(map[string]any); ok {
		return child
	}
	return map[string]any{}
}

// commandList normalizes an event's value to a slice. A non-array value is
// treated as absent.
func commandList(raw any) []any {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	return list
}

// partitionCursorCommands splits an event into the commands cc-clip does not
// own and a count of the ones it does.
func partitionCursorCommands(event []any) ([]any, int) {
	kept := make([]any, 0, len(event))
	managed := 0
	for _, entry := range event {
		if isManagedCursorCommand(entry) {
			managed++
			continue
		}
		kept = append(kept, entry)
	}
	return kept, managed
}

// isManagedCursorCommand reports whether an entry is one cc-clip wrote,
// keying off the ownership prefix so an entry written by an older release is
// still recognized and replaced rather than duplicated.
func isManagedCursorCommand(entry any) bool {
	obj, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := obj["command"].(string)
	return strings.HasPrefix(cmd, cursorManagedHookOwnerPrefix)
}

// cursorCommandIsCurrent reports whether the event's single managed command is
// byte-identical to what this release would write. Only then is the merge a
// no-op; a legacy command still has to be rewritten.
func cursorCommandIsCurrent(event []any) bool {
	for _, entry := range event {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := obj["command"].(string)
		if !strings.HasPrefix(cmd, cursorManagedHookOwnerPrefix) {
			continue
		}
		if cmd != cursorManagedHookCommand {
			return false
		}
		timeout, _ := obj["timeout"].(float64)
		return int(timeout) == cursorHookTimeoutSeconds
	}
	return false
}

func newCursorManagedCommand() map[string]any {
	return map[string]any{
		"command": cursorManagedHookCommand,
		"timeout": cursorHookTimeoutSeconds,
	}
}
