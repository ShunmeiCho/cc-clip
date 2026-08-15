package shim

import "fmt"

// wlCopyShimTemplate is the Wayland WRITE-side companion to the wl-paste shim
// (#128 phase 2). wl-paste covers reads; remote tools COPY through wl-copy
// (neovim's wl-clipboard provider, tmux copy-command, TUI copy actions), so a
// wayland install ships both shims. Dual-write: the payload is forwarded to
// the LOCAL clipboard through the tunnel, then replayed into the real wl-copy
// so remote behavior is unchanged. Only plain clipboard TEXT writes are
// forwarded — --primary, --clear, non-text --type, -v/-h and unknown options
// pass through untouched.
const wlCopyShimTemplate = `#!/bin/bash
# cc-clip wl-copy shim - forwards remote clipboard writes to the local machine (Wayland)
# Installed by: cc-clip install
# Remove with:  cc-clip uninstall

set -euo pipefail

CC_CLIP_PORT="${CC_CLIP_PORT:-%d}"
CC_CLIP_ADDR="127.0.0.1:${CC_CLIP_PORT}"
CC_CLIP_TOKEN_FILE="${CC_CLIP_TOKEN_FILE:-${HOME}/.cache/cc-clip/session.token}"
CC_CLIP_SESSION_FILE="${CC_CLIP_SESSION_FILE:-${HOME}/.cache/cc-clip/session.id}"
CC_CLIP_PROBE_TIMEOUT_MS="${CC_CLIP_PROBE_TIMEOUT_MS:-500}"
CC_CLIP_FETCH_TIMEOUT_MS="${CC_CLIP_FETCH_TIMEOUT_MS:-5000}"
REAL_WL_COPY="%s"
_CC_CLIP_SELF_PATH="${BASH_SOURCE[0]:-$0}"
case "$_CC_CLIP_SELF_PATH" in
    */*) _CC_CLIP_SELF_DIR="${_CC_CLIP_SELF_PATH%%/*}" ;;
    *) _CC_CLIP_SELF_DIR="$(pwd)" ;;
esac
if ! _CC_CLIP_SELF_DIR="$(cd "$_CC_CLIP_SELF_DIR" 2>/dev/null && pwd)"; then
    _CC_CLIP_SELF_DIR="$(pwd)"
fi
_CC_CLIP_SELF_FILE="$_CC_CLIP_SELF_DIR/${_CC_CLIP_SELF_PATH##*/}"

_cc_clip_log() {
    if [ "${CC_CLIP_DEBUG:-}" = "1" ]; then
        echo "cc-clip-shim: $*" >&2
    fi
}

_cc_clip_resolve_real_wl_copy() {
    if [ -n "${REAL_WL_COPY:-}" ] && [ -x "$REAL_WL_COPY" ]; then
        local real_parent real_name real_dir real_path
        case "$REAL_WL_COPY" in
            */*) real_parent="${REAL_WL_COPY%%/*}"; real_name="${REAL_WL_COPY##*/}" ;;
            *) real_parent="."; real_name="$REAL_WL_COPY" ;;
        esac
        real_dir="$(cd "$real_parent" 2>/dev/null && pwd)" || real_dir=""
        real_path="$real_dir/$real_name"
        if [ "$real_path" != "$_CC_CLIP_SELF_FILE" ]; then
            printf '%%s\n' "$REAL_WL_COPY"
            return 0
        fi
    fi

    local old_ifs="$IFS"
    IFS=:
    local dir
    for dir in $PATH; do
        [ -n "$dir" ] || dir="."
        local abs_dir
        abs_dir="$(cd "$dir" 2>/dev/null && pwd)" || continue
        [ "$abs_dir" = "$_CC_CLIP_SELF_DIR" ] && continue
        if [ -x "$abs_dir/wl-copy" ] && [ ! -d "$abs_dir/wl-copy" ]; then
            IFS="$old_ifs"
            printf '%%s\n' "$abs_dir/wl-copy"
            return 0
        fi
    done
    IFS="$old_ifs"
    return 1
}

_cc_clip_fallback() {
    local real_wl_copy
    if ! real_wl_copy="$(_cc_clip_resolve_real_wl_copy)"; then
        echo "cc-clip-shim: real wl-copy binary not found; install wl-clipboard or remove the cc-clip shim" >&2
        exit 127
    fi
    _cc_clip_log "falling back to real wl-copy: $real_wl_copy $*"
    exec "$real_wl_copy" "$@"
}

_cc_clip_read_token() {
    if [ ! -f "$CC_CLIP_TOKEN_FILE" ]; then
        return 1
    fi
    cat "$CC_CLIP_TOKEN_FILE"
}

_cc_clip_session_header() {
    if [ -f "$CC_CLIP_SESSION_FILE" ]; then
        echo "X-CC-Clip-Session: $(cat "$CC_CLIP_SESSION_FILE" 2>/dev/null)"
    fi
}

_cc_clip_curl_config() {
    local token="$1"
    local session_hdr="${2:-}"
    printf 'header = "Authorization: Bearer %%s"\n' "$token"
    printf 'header = "User-Agent: cc-clip/0.1"\n'
    if [ -n "$session_hdr" ]; then
        printf 'header = "%%s"\n' "$session_hdr"
    fi
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

_cc_clip_post_text() {
    local file="$1"
    local token
    token=$(_cc_clip_read_token) || return 12
    local timeout_s
    timeout_s=$(awk "BEGIN {printf \"%%f\", ${CC_CLIP_FETCH_TIMEOUT_MS}/1000}")
    local session_hdr
    session_hdr=$(_cc_clip_session_header)
    _cc_clip_curl_config "$token" "$session_hdr" | curl -sf --max-time "$timeout_s" \
        -X POST -H "Content-Type: text/plain; charset=utf-8" \
        --data-binary @"$file" \
        -K - \
        "http://${CC_CLIP_ADDR}/clipboard/text" >/dev/null
}

# --- option scan (wl-copy semantics) ---
_ORIG=("$@")
primary=0
clear=0
trim=0
mime=""
passthrough=0
positional=()
while [ $# -gt 0 ]; do
    case "$1" in
        -p|--primary) primary=1 ;;
        -c|--clear) clear=1 ;;
        -n|--trim-newline) trim=1 ;;
        -t|--type) mime="${2:-}"; shift ;;
        --type=*) mime="${1#--type=}" ;;
        -s|--seat) shift ;;
        --seat=*) ;;
        -f|--foreground|-o|--paste-once) ;;
        -v|--version|-h|--help) passthrough=1 ;;
        --) shift; while [ $# -gt 0 ]; do positional+=("$1"); shift; done; break ;;
        -*) passthrough=1 ;;
        *) positional+=("$1") ;;
    esac
    if [ $# -gt 0 ]; then shift; fi
done

# Only plain clipboard text writes are forwarded; everything else keeps the
# exact real-binary behavior.
if [ "$passthrough" -eq 1 ] || [ "$primary" -eq 1 ] || [ "$clear" -eq 1 ]; then
    _cc_clip_fallback ${_ORIG[@]+"${_ORIG[@]}"}
fi
case "$mime" in
    ""|text/*|TEXT|STRING|UTF8_STRING) ;;
    *) _cc_clip_fallback ${_ORIG[@]+"${_ORIG[@]}"} ;;
esac

_cc_clip_log "intercepting wl-copy clipboard write (dual-write)"
wtmp=$(mktemp 2>/dev/null) || _cc_clip_fallback ${_ORIG[@]+"${_ORIG[@]}"}
stdin_replay=1
if [ "${#positional[@]}" -gt 0 ]; then
    # wl-copy joins text arguments with single spaces, no trailing newline.
    printf '%%s' "${positional[*]}" > "$wtmp"
    stdin_replay=0
else
    cat > "$wtmp"
fi

# -n/--trim-newline: mirror it on the forwarded payload so local and remote
# clipboards end up byte-identical. The replay below feeds the ORIGINAL bytes
# and lets the real wl-copy apply its own trim.
posttmp="$wtmp"
if [ "$trim" -eq 1 ] && [ -s "$wtmp" ] && [ -z "$(tail -c 1 "$wtmp")" ]; then
    posttmp=$(mktemp 2>/dev/null) || posttmp="$wtmp"
    if [ "$posttmp" != "$wtmp" ]; then
        head -c "$(( $(wc -c < "$wtmp") - 1 ))" "$wtmp" > "$posttmp"
    fi
fi

forwarded=0
if _cc_clip_probe && _cc_clip_post_text "$posttmp"; then
    forwarded=1
    _cc_clip_log "forwarded to local clipboard"
else
    _cc_clip_log "local forward failed or tunnel down"
fi

real_rc=0
if wreal="$(_cc_clip_resolve_real_wl_copy)"; then
    if [ "$stdin_replay" -eq 1 ]; then
        "$wreal" ${_ORIG[@]+"${_ORIG[@]}"} < "$wtmp" || real_rc=$?
    else
        "$wreal" ${_ORIG[@]+"${_ORIG[@]}"} || real_rc=$?
    fi
else
    real_rc=127
fi
if [ "$posttmp" != "$wtmp" ]; then rm -f "$posttmp"; fi
rm -f "$wtmp"

# Success if EITHER side took the copy: a headless remote with a working
# tunnel is the primary use case and must not fail the caller.
if [ "$real_rc" -eq 0 ] || [ "$forwarded" -eq 1 ]; then
    exit 0
fi
exit "$real_rc"
`

// WlCopyShim renders the wl-copy shim script.
func WlCopyShim(port int, realWlCopyPath string) string {
	return fmt.Sprintf(wlCopyShimTemplate, port, realWlCopyPath)
}
