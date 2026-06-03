package shim

const claudeWrapperTemplate = `#!/usr/bin/env bash
# cc-clip claude wrapper — fallback notification hook injector
# Installed by: cc-clip connect
# Remove with:  cc-clip uninstall --host <this-host>

# Find the real claude binary.
# Priority 1: ~/.local/bin/claude.cc-clip-real (the sidecar set by install).
# Priority 2: walk $PATH skipping our own directory (legacy install fallback).
_REAL_CLAUDE=""
_SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
_SIDECAR="$_SELF_DIR/claude.cc-clip-real"
_NO_HOOKS_FILE="${HOME}/.cache/cc-clip/no-hooks"

if [ -x "$_SIDECAR" ]; then
    _REAL_CLAUDE="$_SIDECAR"
else
    IFS=: read -ra _PATH_DIRS <<< "$PATH"
    for _dir in "${_PATH_DIRS[@]}"; do
        [ "$_dir" = "$_SELF_DIR" ] && continue
        [ -x "$_dir/claude" ] && _REAL_CLAUDE="$_dir/claude" && break
    done
fi

if [ -z "$_REAL_CLAUDE" ]; then
    echo "cc-clip: real claude binary not found (no sidecar at $_SIDECAR and no claude on PATH outside $_SELF_DIR)" >&2
    exit 1
fi

if [ -f "$_NO_HOOKS_FILE" ]; then
    exec "$_REAL_CLAUDE" "$@"
fi

exec "$_REAL_CLAUDE" --settings '{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type":"command","command":"cc-clip-hook"}]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [{"type":"command","command":"cc-clip-hook"}]
      }
    ]
  }
}' "$@"
`

// ClaudeWrapperScript returns the fallback claude wrapper bash script. New
// installs prefer durable hooks in ~/.claude/settings.json; this wrapper is
// retained only for settings merge failures and older deployments. The hook
// script itself is fail-soft when the tunnel is unavailable.
func ClaudeWrapperScript(port int) string {
	_ = port
	return claudeWrapperTemplate
}
