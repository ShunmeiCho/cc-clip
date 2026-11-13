//go:build !windows

package tunnelmgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthenticationUsesForwardingFreeOwnedMaster(t *testing.T) {
	for mode, want := range map[string]string{"master": "ok", "authfail": "auth-required", "hostkey": "host-key-error", "connectfail": "connection-error"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FAKE_SSH_MODE", mode)
			b := &Backend{Spec: serviceConfig("fake-host").Spec, SSHBinary: writeFakeSSH(t), ControlDir: t.TempDir()}
			result := checkAuthentication(context.Background(), b)
			if result.State != want || !result.Valid() {
				t.Fatalf("authentication = %+v, want %s", result, want)
			}
			if b.Running() || b.Forwarded() {
				t.Fatal("authentication left a child or forward")
			}
			if _, err := os.Stat(b.ControlPath()); !os.IsNotExist(err) {
				t.Fatalf("control socket survived: %v", err)
			}
		})
	}
}

func TestAuthenticationTimeoutFailsClosed(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_CHECK_MODE", "hang")
	b := &Backend{Spec: serviceConfig("fake-host").Spec, SSHBinary: writeFakeSSH(t), ControlDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if result := checkAuthentication(ctx, b); result.State != "timeout" {
		t.Fatalf("result = %+v", result)
	}
	if b.Running() {
		t.Fatal("timed-out authentication child survived")
	}
	if (AuthResult{State: "healthy"}).Valid() {
		t.Fatal("tunnel health was accepted as proof of authentication")
	}
}

func TestAuthenticationCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &Backend{Spec: serviceConfig("fake-host").Spec, SSHBinary: writeFakeSSH(t), ControlDir: t.TempDir()}
	if result := checkAuthentication(ctx, b); result.State != "timeout" {
		t.Fatalf("result = %+v", result)
	}
	if b.ControlPath() != "" || b.Done() != nil {
		t.Fatal("canceled check spawned an SSH master")
	}
}

func TestAuthenticationOptionsOverrideUnsafeSSHConfig(t *testing.T) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH is required to verify effective authentication options")
	}
	config := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(config, []byte("Host auth-test\n  HostName resolved.invalid\n  BatchMode no\n  StrictHostKeyChecking no\n  ForkAfterAuthentication yes\n  ControlPersist yes\n  RemoteForward 18399 127.0.0.1:18399\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b := &Backend{Spec: serviceConfig("auth-test").Spec}
	args := append([]string{"-G", "-F", config}, b.MasterArgs("/cc-clip-auth-test.sock")...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ssh, args...).Output()
	if err != nil {
		t.Fatalf("inspect effective SSH configuration: %v", err)
	}
	options := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, _ := strings.Cut(line, " ")
		options[key] = value
	}
	for key, want := range map[string]string{"hostname": "resolved.invalid", "batchmode": "yes", "stricthostkeychecking": "true", "forkafterauthentication": "no", "controlpersist": "no", "clearallforwardings": "yes"} {
		if options[key] != want {
			t.Errorf("effective %s = %q, want %q", key, options[key], want)
		}
	}
	if options["remoteforward"] != "" {
		t.Fatal("authentication inherited a reverse forward")
	}
}
