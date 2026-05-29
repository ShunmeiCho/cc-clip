package shim

import "fmt"

const hookTemplate = `#!/usr/bin/env bash
# cc-clip-hook — Claude Code hook bridge
# Reads hook JSON from stdin, forwards to cc-clip daemon via tunnel

set -euo pipefail

_CC_CLIP_PORT="${CC_CLIP_PORT:-%d}"
_CC_CLIP_CACHE_DIR="${HOME}/.cache/cc-clip"
_CC_CLIP_NONCE_FILE="${_CC_CLIP_CACHE_DIR}/notify.nonce"
_CC_CLIP_HOST_ALIAS="${CC_CLIP_HOST_ALIAS:-$(hostname -s)}"
_CC_CLIP_HEALTH_FILE="${_CC_CLIP_CACHE_DIR}/notify-health.log"

mkdir -p "$_CC_CLIP_CACHE_DIR" 2>/dev/null || true

_nonce=""
if [ -f "$_CC_CLIP_NONCE_FILE" ]; then
	_nonce=$(head -1 "$_CC_CLIP_NONCE_FILE")
fi

_payload=$(cat)

_payload=$(echo "$_payload" | CC_CLIP_HOST="$_CC_CLIP_HOST_ALIAS" python3 -c '
import sys, json, os
d = json.load(sys.stdin)
d["_cc_clip_host"] = os.environ["CC_CLIP_HOST"]
json.dump(d, sys.stdout)
	' 2>/dev/null || echo "$_payload")

_payload_file=$(mktemp "${_CC_CLIP_CACHE_DIR}/notify-payload.XXXXXX" 2>/dev/null || mktemp 2>/dev/null || true)
if [ -z "$_payload_file" ]; then
	echo "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) FAIL payload_tmp" >> "$_CC_CLIP_HEALTH_FILE" 2>/dev/null || true
	exit 0
fi
trap 'rm -f "$_payload_file"' EXIT
chmod 600 "$_payload_file" 2>/dev/null || true
if ! printf '%%s' "$_payload" > "$_payload_file"; then
	echo "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) FAIL payload_write" >> "$_CC_CLIP_HEALTH_FILE" 2>/dev/null || true
	exit 0
fi

_cc_clip_curl_config() {
	printf 'header = "Authorization: Bearer %%s"\n' "$_nonce"
	printf 'header = "Content-Type: application/x-claude-hook"\n'
	printf 'header = "User-Agent: cc-clip-hook/0.1"\n'
}

_http_code=$(_cc_clip_curl_config | curl -sf --connect-timeout 2 --max-time 5 -K - -o /dev/null -w '%%{http_code}' -X POST \
	--data-binary "@$_payload_file" \
	"http://127.0.0.1:${_CC_CLIP_PORT}/notify" \
	2>/dev/null) || _http_code="000"

if [ "$_http_code" != "204" ] && [ "$_http_code" != "200" ]; then
	echo "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) FAIL http=$_http_code" >> "$_CC_CLIP_HEALTH_FILE" 2>/dev/null || true
fi

exit 0
`

// HookScript returns the cc-clip-hook bash script with the given port baked in.
// This script is installed to ~/.local/bin/cc-clip-hook on the remote. Claude Code
// hooks pipe JSON to stdin, which the script forwards to the cc-clip daemon via
// the SSH tunnel. Authentication uses the notification nonce (not the clipboard
// session token).
func HookScript(port int) string {
	return fmt.Sprintf(hookTemplate, port)
}
