//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/service"
	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

// Opt in because this test registers short-lived jobs in the current user's
// real GUI domain. The SSH fixture never connects to a network or reads keys.
func TestTunnelAuthenticationRealLaunchAgent(t *testing.T) {
	if os.Getenv("CC_CLIP_TEST_LAUNCHD") != "1" {
		t.Skip("set CC_CLIP_TEST_LAUNCHD=1 to exercise the real GUI launchd domain")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"ok", "auth-required", "host-key-error"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SSH_AUTH_SOCK", "/cc-clip-test-shell-only/agent.sock")
			t.Setenv("CC_CLIP_TEST_SHELL_ONLY", "present")
			// The wrapper dispatches the test executable as the hidden helper.
			// It preserves the environment launchd actually supplies.
			binary := filepath.Join(home, "cc-clip")
			wrapper := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run '^TestTunnelLaunchdAuthenticationHelper$' -- \"$@\"\n", strings.ReplaceAll(executable, "'", "'\"'\"'"))
			if err := os.WriteFile(binary, []byte(wrapper), 0700); err != nil {
				t.Fatal(err)
			}
			writeLaunchdSSHFixture(t, home)
			manager, err := service.NewTunnelManager()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			spec := tunnelmgr.Spec{Host: state, Port: 18399, ExpectedInstanceID: "authentication-only"}
			result, err := manager.Preflight(ctx, binary, spec)
			if err != nil || result.State != state {
				t.Fatalf("real launchd authentication = %+v, error=%v", result, err)
			}
			if data, err := os.ReadFile(filepath.Join(home, "context-verified")); err != nil || string(data) != "gui environment and private umask verified" {
				t.Fatalf("helper did not verify service context: %v", err)
			}
			root := filepath.Join(home, ".cache", "cc-clip", "tunnel-runtime", "auth")
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("preflight artifacts survived: %v, %v", entries, err)
			}
			entries, err = os.ReadDir(filepath.Join(home, "ssh-children"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("authentication SSH child survived: %v, %v", entries, err)
			}
		})
	}
}

func TestTunnelLaunchdAuthenticationHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == -1 {
		t.Skip("only runs as a one-shot launchd helper")
	}
	args := os.Args[separator+1:]
	if len(args) != 6 || args[0] != "tunnel" || args[1] != "check-auth" {
		t.Fatalf("unexpected helper arguments: %v", args)
	}
	home := os.Getenv("HOME")
	if !strings.HasPrefix(args[5], filepath.Join(home, ".cache", "cc-clip", "tunnel-runtime", "auth")+string(filepath.Separator)) {
		t.Fatal("helper HOME differs from the job's authentication directory")
	}
	if os.Getenv("PATH") != "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" || os.Getenv("SSH_AUTH_SOCK") == "/cc-clip-test-shell-only/agent.sock" || os.Getenv("CC_CLIP_TEST_SHELL_ONLY") != "" {
		t.Fatal("launchd helper inherited the terminal environment")
	}
	mask := syscall.Umask(0077)
	syscall.Umask(mask)
	if mask != 0077 {
		t.Fatalf("helper umask = %03o, want 077", mask)
	}
	if err := os.WriteFile(filepath.Join(home, "context-verified"), []byte("gui environment and private umask verified"), 0600); err != nil {
		t.Fatal(err)
	}
	// Substitute only the SSH executable after verifying the real launchd
	// environment. Exercise the production helper, result publication and
	// backend with deterministic authentication failures and no real credentials.
	t.Setenv("PATH", filepath.Join(home, "fake-bin")+":"+os.Getenv("PATH"))
	cmdTunnelCheckAuth(args[2:])
	data, err := os.ReadFile(filepath.Join(args[5], "auth-result.json"))
	var result tunnelmgr.AuthResult
	if err != nil || json.Unmarshal(data, &result) != nil || !result.Valid() {
		t.Fatalf("helper did not publish a valid authentication result: %v", err)
	}
}

func writeLaunchdSSHFixture(t *testing.T, home string) {
	t.Helper()
	for _, dir := range []string{"fake-bin", "ssh-children"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
socket=""
operation=""
master=0
previous=""
for arg in "$@"; do
  [ "$previous" = "-S" ] && socket="$arg"
  [ "$previous" = "-O" ] && operation="$arg"
  [ "$arg" = "-N" ] && master=1
  previous="$arg"
done
if [ "$master" = 1 ]; then
  case "$previous" in
    auth-required) echo 'Permission denied (publickey,password).' >&2; exit 255 ;;
    host-key-error) echo 'Host key verification failed.' >&2; exit 255 ;;
  esac
  marker="$HOME/ssh-children/$$"
  echo $$ > "$socket"
  touch "$marker"
  trap 'rm -f "$marker" "$socket"; exit 0' TERM
  while :; do sleep 0.05; done
fi
case "$operation" in
  check) [ -r "$socket" ] && kill -0 "$(cat "$socket")" 2>/dev/null; exit $? ;;
  exit) [ -r "$socket" ] && kill -TERM "$(cat "$socket")" 2>/dev/null; exit $? ;;
esac
echo 'unexpected operation or forwarding request' >&2
exit 255
`
	if err := os.WriteFile(filepath.Join(home, "fake-bin", "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveTunnelAuthDelegatesSafelyToOpenSSH(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh")
	// Never read stdin: record only the public argument list, then reject auth.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CC_CLIP_TEST_AUTH_ARGV\"\nexit 255\n"), 0700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "argv")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("CC_CLIP_TEST_AUTH_ARGV", record)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := interactiveTunnelAuth(ctx, "example-host"); err == nil {
		t.Fatal("foreground SSH failure was accepted")
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, option := range []string{"BatchMode=no", "ClearAllForwardings=yes", "ControlMaster=no", "ControlPath=none", "ForwardAgent=no", "ForwardX11=no", "StrictHostKeyChecking=ask"} {
		if !strings.Contains("\n"+string(data), "\n"+option+"\n") {
			t.Errorf("foreground authentication missing %s", option)
		}
	}
	if strings.Join(args[len(args)-3:], " ") != "-- example-host true" {
		t.Fatalf("unsafe host or remote command: %v", args)
	}
}
