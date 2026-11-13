package tunnelmgr

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/shim"
)

// TestMasterArgs pins the exact ssh invocation of the managed master. The
// option set is the safety boundary: the master inherits connection settings
// from the named Host but starts with no forwarding at all.
func TestMasterArgs(t *testing.T) {
	b := &Backend{Spec: Spec{Host: "example-host", Port: 18339, ExpectedInstanceID: "instance-123"}}
	args := b.MasterArgs("/ctl/dir/ctl-ab12.sock")

	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"-M", "-N", "-T", "-S", "/ctl/dir/ctl-ab12.sock",
		"BatchMode=yes",
		"StrictHostKeyChecking=yes",
		"ForkAfterAuthentication=no",
		"ClearAllForwardings=yes",
		"ExitOnForwardFailure=yes",
		"ControlMaster=yes",
		"ControlPersist=no",
		"ForwardAgent=no",
		"ForwardX11=no",
		"ServerAliveInterval=15",
		"ServerAliveCountMax=3",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("master argv missing %q; got %q", want, args)
		}
	}
	if args[len(args)-1] != "example-host" {
		t.Errorf("host must be the final argument, got %q", args[len(args)-1])
	}
	if strings.Contains(joined, "-R") {
		t.Errorf("master argv must be forwarding-free: %q", args)
	}
	if args[len(args)-2] != "--" {
		t.Errorf("host must follow option terminator: %q", args)
	}
}

func TestForwardControlArgsAddsOnlyManagedReverseForward(t *testing.T) {
	b := &Backend{Spec: Spec{Host: "example-host", Port: 18339, ExpectedInstanceID: "instance-123"}}
	args := b.controlArgs("/ctl/empty", "/ctl/socket", "forward", "-R", "127.0.0.1:18339:127.0.0.1:18339")
	joined := strings.Join(args, "\x00")
	for _, want := range []string{"-F\x00/ctl/empty", "-S\x00/ctl/socket", "-O\x00forward", "-R\x00127.0.0.1:18339:127.0.0.1:18339", "--\x00example-host"} {
		if !strings.Contains(joined, want) {
			t.Errorf("forward control argv missing %q: %q", want, args)
		}
	}
}

func TestProbeArgs(t *testing.T) {
	b := &Backend{Spec: Spec{Host: "example-host", Port: 18339, ExpectedInstanceID: "instance-123"}}
	cmd := "remote probe script"
	args := b.probeArgs("/ctl/dir/empty-config", "/ctl/dir/ctl-ab12.sock", cmd)

	joined := strings.Join(args, "\x00")
	for _, want := range []string{"BatchMode=yes", "ClearAllForwardings=yes", "/ctl/dir/empty-config", "/ctl/dir/ctl-ab12.sock"} {
		if !strings.Contains(joined, want) {
			t.Errorf("probe argv missing %q; got %q", want, args)
		}
	}
	// The probe session must not open or close any forwarding of its own.
	if strings.Contains(joined, "-R") || strings.Contains(joined, "-N") {
		t.Errorf("probe argv must not carry forwarding flags: %q", args)
	}
	if got, want := args[len(args)-1], shim.WrapRemoteShell(cmd); got != want {
		t.Errorf("probe command must be wrapped as the final argument\n got: %q\nwant: %q", got, want)
	}
	if args[len(args)-2] != "example-host" {
		t.Errorf("probe target host wrong: %q", args)
	}
}

func TestNewControlSocketPathUniqueAndInsideDir(t *testing.T) {
	dir := t.TempDir()
	first, err := newControlSocketPath(dir)
	if err != nil {
		t.Fatalf("first path: %v", err)
	}
	second, err := newControlSocketPath(dir)
	if err != nil {
		t.Fatalf("second path: %v", err)
	}
	if first == second {
		t.Fatalf("control socket paths must be unique per start, got %q twice", first)
	}
	if filepath.Dir(first) != dir {
		t.Fatalf("socket path %q escapes control dir %q", first, dir)
	}
	// Reserve leaves no file behind: ssh creates the socket itself.
	if _, err := os.Stat(first); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reserved path must not exist, stat err = %v", err)
	}
}

func TestNewEmptyControlConfigIsPrivateAndEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-ssh-config")
	got, err := newEmptyControlConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("config path = %q, want %q", got, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("empty config size = %d, want 0", info.Size())
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != stateFileMode {
		t.Fatalf("empty config mode = %o, want %o", got, stateFileMode)
	}
}
