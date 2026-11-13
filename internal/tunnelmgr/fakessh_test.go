//go:build !windows

package tunnelmgr

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFakeSSH writes a /bin/sh script that emulates the OpenSSH surface the
// backend and supervisor exercise: a long-running master (invoked with -N)
// and one-shot probe clients multiplexed over the ControlPath. Behavior is
// selected through environment variables so each test can script failure
// modes (auth refused, host key rejected, master dies after N seconds).
//
// This file is excluded on Windows by its build tag (no /bin/sh there); the
// CI Windows job still compiles the whole package, and the argv/store logic
// these tests cover is platform-independent.
func writeFakeSSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh")
	script := `#!/bin/sh
has_n=0
cp=""
op=""
prev=""
for a in "$@"; do
  [ "$a" = "-N" ] && has_n=1
  [ "$prev" = "-S" ] && cp="$a"
  [ "$prev" = "-O" ] && op="$a"
  case "$a" in ControlPath=*) cp="${a#ControlPath=}" ;; esac
  prev="$a"
done
if [ "$has_n" = "1" ]; then
  [ -n "$FAKE_SSH_COUNT_FILE" ] && echo x >> "$FAKE_SSH_COUNT_FILE"
  case "$FAKE_SSH_MODE" in
    authfail) echo "ssh: Permission denied (publickey,password)." >&2; exit 255 ;;
    hostkey)  echo "Host key verification failed." >&2; exit 255 ;;
    configfail) echo "Bad configuration option: invalid-option" >&2; exit 255 ;;
    secretfail) printf '%s\n' "$FAKE_SSH_SECRET" >&2; echo "Permission denied (publickey)." >&2; exit 255 ;;
    connectfail) echo "ssh: connect to host fake-host: Connection refused" >&2; exit 255 ;;
    die)      [ -n "$cp" ] && echo $$ > "$cp"; sleep "${FAKE_SSH_UPTIME:-0}"; echo "Connection closed by remote host." >&2; exit 255 ;;
    *)        [ -n "$cp" ] && echo $$ > "$cp"; trap 'rm -f "$cp"; exit 0' TERM; while :; do sleep 0.05; done ;;
  esac
fi
case "$op" in
  check)
	[ "$FAKE_SSH_CHECK_MODE" = "hang" ] && exec sleep 30
	[ "$FAKE_SSH_CHECK_MODE" = "after-conflict" ] && [ -e "$cp.conflict" ] && exit 255
    [ -r "$cp" ] || exit 255
    kill -0 "$(cat "$cp")" 2>/dev/null || exit 255
    exit 0
    ;;
  forward)
    case "$FAKE_SSH_FORWARD_MODE" in
	  conflict) touch "$cp.conflict"; echo "remote port forwarding failed for listen port 18399" >&2; exit 255 ;;
      fail) echo "forward request failed" >&2; exit 255 ;;
      *) exit 0 ;;
    esac
    ;;
  cancel) exit 0 ;;
  exit)
    [ -r "$cp" ] && kill -TERM "$(cat "$cp")" 2>/dev/null
    exit 0
    ;;
esac
case "$*" in
  *probe-identity*)
    if [ -n "$FAKE_SSH_IDENTITY_HELPER" ]; then
      exec "$FAKE_SSH_IDENTITY_HELPER" -test.run '^TestSupervisorDaemonIdentityHelper$'
    fi
    printf '%s' "${FAKE_SSH_IDENTITY_OUT:-cc-clip-identity:ok:{\"service\":\"cc-clip\",\"status\":\"ok\",\"protocol_version\":1,\"instance_id\":\"instance-123\"}}"; exit "${FAKE_SSH_IDENTITY_EXIT:-0}" ;;
esac
printf '%s' "$FAKE_SSH_PROBE_OUT"
exit "${FAKE_SSH_PROBE_EXIT:-0}"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	return path
}
