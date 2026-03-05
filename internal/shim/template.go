package shim

import "fmt"

const xclipShimTemplate = `#!/bin/bash
# cc-clip xclip shim - intercepts Claude Code clipboard calls
# Installed by: cc-clip install
# Remove with:  cc-clip uninstall

set -euo pipefail

CC_CLIP_PORT="${CC_CLIP_PORT:-%d}"
CC_CLIP_ADDR="127.0.0.1:${CC_CLIP_PORT}"
CC_CLIP_TOKEN_FILE="${CC_CLIP_TOKEN_FILE:-${HOME}/.cache/cc-clip/session.token}"
CC_CLIP_PROBE_TIMEOUT_MS="${CC_CLIP_PROBE_TIMEOUT_MS:-500}"
CC_CLIP_FETCH_TIMEOUT_MS="${CC_CLIP_FETCH_TIMEOUT_MS:-5000}"
CC_CLIP_TOTAL_TIMEOUT_MS="${CC_CLIP_TOTAL_TIMEOUT_MS:-8000}"
REAL_XCLIP="%s"

_cc_clip_log() {
    if [ "${CC_CLIP_DEBUG:-}" = "1" ]; then
        echo "cc-clip-shim: $*" >&2
    fi
}

_cc_clip_fallback() {
    _cc_clip_log "falling back to real xclip: $REAL_XCLIP $*"
    exec "$REAL_XCLIP" "$@"
}

_cc_clip_read_token() {
    if [ ! -f "$CC_CLIP_TOKEN_FILE" ]; then
        return 1
    fi
    cat "$CC_CLIP_TOKEN_FILE"
}

_cc_clip_probe() {
    local timeout_s
    timeout_s=$(awk "BEGIN {printf \"%%f\", ${CC_CLIP_PROBE_TIMEOUT_MS}/1000}")
    if command -v timeout >/dev/null 2>&1; then
        timeout "$timeout_s" bash -c "echo >/dev/tcp/${CC_CLIP_ADDR%%%%:*}/${CC_CLIP_ADDR##*:}" 2>/dev/null
    elif command -v nc >/dev/null 2>&1; then
        nc -z -w 1 "${CC_CLIP_ADDR%%%%:*}" "${CC_CLIP_ADDR##*:}" 2>/dev/null
    else
        bash -c "echo >/dev/tcp/${CC_CLIP_ADDR%%%%:*}/${CC_CLIP_ADDR##*:}" 2>/dev/null
    fi
}

# Fetch JSON endpoint (text-safe, small payloads only)
_cc_clip_fetch_json() {
    local path="$1"
    local token
    token=$(_cc_clip_read_token) || return 12
    local timeout_s
    timeout_s=$(awk "BEGIN {printf \"%%f\", ${CC_CLIP_FETCH_TIMEOUT_MS}/1000}")
    curl -sf --max-time "$timeout_s" \
        -H "Authorization: Bearer ${token}" \
        -H "User-Agent: cc-clip/0.1" \
        "http://${CC_CLIP_ADDR}${path}"
}

# Fetch binary to temp file, then cat to stdout (preserves NUL bytes, allows fallback)
_cc_clip_fetch_binary() {
    local path="$1"
    local token
    token=$(_cc_clip_read_token) || return 12
    local timeout_s
    timeout_s=$(awk "BEGIN {printf \"%%f\", ${CC_CLIP_FETCH_TIMEOUT_MS}/1000}")
    local tmpfile
    tmpfile=$(mktemp 2>/dev/null) || return 20
    if curl -sf --max-time "$timeout_s" \
        -o "$tmpfile" \
        -H "Authorization: Bearer ${token}" \
        -H "User-Agent: cc-clip/0.1" \
        "http://${CC_CLIP_ADDR}${path}"; then
        cat "$tmpfile"
        rm -f "$tmpfile"
        return 0
    else
        local rc=$?
        rm -f "$tmpfile"
        return $rc
    fi
}

# Parse arguments to detect Claude Code invocation patterns
ARGS="$*"

case "$ARGS" in
    *"-selection clipboard"*"-t TARGETS"*"-o"*)
        # Claude checks clipboard targets
        _cc_clip_log "intercepting TARGETS check"
        if _cc_clip_probe; then
            RESULT=$(_cc_clip_fetch_json "/clipboard/type" 2>/dev/null) || {
                _cc_clip_log "fetch type failed, exit=$?"
                _cc_clip_fallback "$@"
            }
            TYPE=$(echo "$RESULT" | grep -o '"type":"[^"]*"' | head -1 | cut -d'"' -f4)
            if [ "$TYPE" = "image" ]; then
                FORMAT=$(echo "$RESULT" | grep -o '"format":"[^"]*"' | head -1 | cut -d'"' -f4)
                echo "image/${FORMAT:-png}"
                exit 0
            fi
            _cc_clip_fallback "$@"
        else
            _cc_clip_log "tunnel not reachable"
            _cc_clip_fallback "$@"
        fi
        ;;

    *"-selection clipboard"*"-t image/"*"-o"*)
        # Claude reads clipboard image — fetch to temp file then cat (binary-safe + fallback-safe)
        _cc_clip_log "intercepting image read"
        if _cc_clip_probe; then
            if _cc_clip_fetch_binary "/clipboard/image"; then
                exit 0
            fi
            _cc_clip_log "fetch image failed, falling back"
            _cc_clip_fallback "$@"
        else
            _cc_clip_log "tunnel not reachable"
            _cc_clip_fallback "$@"
        fi
        ;;

    *)
        # All other invocations: pass through
        _cc_clip_fallback "$@"
        ;;
esac
`

func XclipShim(port int, realXclipPath string) string {
	return fmt.Sprintf(xclipShimTemplate, port, realXclipPath)
}

const wlPasteShimTemplate = `#!/bin/bash
# cc-clip wl-paste shim - intercepts Claude Code clipboard calls (Wayland)
# Installed by: cc-clip install
# Remove with:  cc-clip uninstall

set -euo pipefail

CC_CLIP_PORT="${CC_CLIP_PORT:-%d}"
CC_CLIP_ADDR="127.0.0.1:${CC_CLIP_PORT}"
CC_CLIP_TOKEN_FILE="${CC_CLIP_TOKEN_FILE:-${HOME}/.cache/cc-clip/session.token}"
CC_CLIP_PROBE_TIMEOUT_MS="${CC_CLIP_PROBE_TIMEOUT_MS:-500}"
CC_CLIP_FETCH_TIMEOUT_MS="${CC_CLIP_FETCH_TIMEOUT_MS:-5000}"
REAL_WL_PASTE="%s"

_cc_clip_log() {
    if [ "${CC_CLIP_DEBUG:-}" = "1" ]; then
        echo "cc-clip-shim: $*" >&2
    fi
}

_cc_clip_fallback() {
    _cc_clip_log "falling back to real wl-paste: $REAL_WL_PASTE $*"
    exec "$REAL_WL_PASTE" "$@"
}

_cc_clip_read_token() {
    if [ ! -f "$CC_CLIP_TOKEN_FILE" ]; then
        return 1
    fi
    cat "$CC_CLIP_TOKEN_FILE"
}

_cc_clip_probe() {
    if command -v nc >/dev/null 2>&1; then
        nc -z -w 1 "${CC_CLIP_ADDR%%%%:*}" "${CC_CLIP_ADDR##*:}" 2>/dev/null
    else
        bash -c "echo >/dev/tcp/${CC_CLIP_ADDR%%%%:*}/${CC_CLIP_ADDR##*:}" 2>/dev/null
    fi
}

_cc_clip_fetch_json() {
    local path="$1"
    local token
    token=$(cat "$CC_CLIP_TOKEN_FILE" 2>/dev/null) || return 12
    local timeout_s
    timeout_s=$(awk "BEGIN {printf \"%%f\", ${CC_CLIP_FETCH_TIMEOUT_MS}/1000}")
    curl -sf --max-time "$timeout_s" \
        -H "Authorization: Bearer ${token}" \
        -H "User-Agent: cc-clip/0.1" \
        "http://${CC_CLIP_ADDR}${path}"
}

_cc_clip_fetch_binary() {
    local path="$1"
    local token
    token=$(cat "$CC_CLIP_TOKEN_FILE" 2>/dev/null) || return 12
    local timeout_s
    timeout_s=$(awk "BEGIN {printf \"%%f\", ${CC_CLIP_FETCH_TIMEOUT_MS}/1000}")
    local tmpfile
    tmpfile=$(mktemp 2>/dev/null) || return 20
    if curl -sf --max-time "$timeout_s" \
        -o "$tmpfile" \
        -H "Authorization: Bearer ${token}" \
        -H "User-Agent: cc-clip/0.1" \
        "http://${CC_CLIP_ADDR}${path}"; then
        cat "$tmpfile"
        rm -f "$tmpfile"
        return 0
    else
        local rc=$?
        rm -f "$tmpfile"
        return $rc
    fi
}

ARGS="$*"

case "$ARGS" in
    *"--list-types"*)
        # Claude checks available types
        _cc_clip_log "intercepting --list-types"
        if _cc_clip_probe; then
            RESULT=$(_cc_clip_fetch_json "/clipboard/type" 2>/dev/null) || {
                _cc_clip_fallback "$@"
            }
            TYPE=$(echo "$RESULT" | grep -o '"type":"[^"]*"' | head -1 | cut -d'"' -f4)
            if [ "$TYPE" = "image" ]; then
                FORMAT=$(echo "$RESULT" | grep -o '"format":"[^"]*"' | head -1 | cut -d'"' -f4)
                echo "image/${FORMAT:-png}"
                exit 0
            fi
            _cc_clip_fallback "$@"
        else
            _cc_clip_fallback "$@"
        fi
        ;;

    *"--type"*"image/"*)
        # Claude reads image data — fetch to temp file then cat (binary-safe + fallback-safe)
        _cc_clip_log "intercepting image read"
        if _cc_clip_probe; then
            if _cc_clip_fetch_binary "/clipboard/image"; then
                exit 0
            fi
            _cc_clip_log "fetch image failed, falling back"
            _cc_clip_fallback "$@"
        else
            _cc_clip_fallback "$@"
        fi
        ;;

    *)
        _cc_clip_fallback "$@"
        ;;
esac
`

func WlPasteShim(port int, realWlPastePath string) string {
	return fmt.Sprintf(wlPasteShimTemplate, port, realWlPastePath)
}
