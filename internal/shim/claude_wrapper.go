package shim

import "fmt"

const claudeWrapperTemplate = `#!/usr/bin/env bash
# cc-clip claude wrapper — auto-inject notification hooks
# Installed by: cc-clip connect
# Remove with:  cc-clip uninstall --host <this-host>

# Find the real claude binary.
# Priority 1: ~/.local/bin/claude.cc-clip-real (the sidecar set by install).
# Priority 2: walk $PATH skipping our own directory (legacy install fallback).
_REAL_CLAUDE=""
_SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
_SIDECAR="$_SELF_DIR/claude.cc-clip-real"

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

# Only inject hooks if cc-clip tunnel is alive
if curl -sf --connect-timeout 1 --max-time 2 "http://127.0.0.1:${CC_CLIP_PORT:-%d}/health" >/dev/null 2>&1; then
    exec "$_REAL_CLAUDE" --settings '{
  "hooks": {
    "Stop": [{"type":"command","command":"cc-clip-hook"}],
    "Notification": [{"type":"command","command":"cc-clip-hook"}]
  }
}' "$@"
else
    # Tunnel not available — run claude without hook injection
    exec "$_REAL_CLAUDE" "$@"
fi
`

// ClaudeWrapperScript returns the claude wrapper bash script with the
// given port baked in. This script is installed to ~/.local/bin/claude
// on the remote. When the cc-clip tunnel is alive, it injects Stop and
// Notification hooks via --settings so users don't need to manually
// configure hooks in ~/.claude/settings.json. When the tunnel is down,
// it transparently passes through to the real claude binary.
func ClaudeWrapperScript(port int) string {
	return fmt.Sprintf(claudeWrapperTemplate, port)
}
