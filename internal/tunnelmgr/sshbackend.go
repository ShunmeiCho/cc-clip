package tunnelmgr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shunmei/cc-clip/internal/shim"
	"github.com/shunmei/cc-clip/internal/tunnel"
)

const defaultControlDirName = ".cache/cc-clip/tunnel-runtime/control"

var ErrPortConflict = errors.New("remote forward port conflict")

type ExitInfo struct {
	Code        int
	StderrTail  string
	Uptime      time.Duration
	processExit bool
}

// Backend owns exactly one directly spawned SSH master. Forwarding is added
// only after the master is reachable through its private control socket.
type Backend struct {
	Spec       Spec
	SSHBinary  string
	ControlDir string

	mu            sync.Mutex
	cmd           *exec.Cmd
	controlPath   string
	controlConfig string
	controlDir    string
	ephemeralDir  bool
	running       bool
	forwarded     bool
	exit          *ExitInfo
	done          chan struct{}
}

// Start spawns a forwarding-free private master. WaitReady and Forward perform
// the two remaining bootstrap steps explicitly.
func (b *Backend) Start() error {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return fmt.Errorf("managed ssh child already running")
	}
	dir, ephemeralDir, err := b.prepareControlDir()
	if err != nil {
		b.mu.Unlock()
		return err
	}
	controlPath, err := newControlSocketPath(dir)
	if err != nil {
		cleanupControlArtifacts("", "", dir, ephemeralDir)
		b.mu.Unlock()
		return err
	}
	controlConfig, err := newEmptyControlConfig(controlPath + ".config")
	if err != nil {
		cleanupControlArtifacts(controlPath, "", dir, ephemeralDir)
		b.mu.Unlock()
		return err
	}
	cmd := exec.Command(b.sshBinary(), b.MasterArgs(controlPath)...)
	buf := &tailBuffer{max: 4096}
	cmd.Stdout = buf
	cmd.Stderr = buf
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		cleanupControlArtifacts(controlPath, controlConfig, dir, ephemeralDir)
		b.mu.Unlock()
		return fmt.Errorf("start ssh master: %w", err)
	}
	b.cmd = cmd
	b.controlPath = controlPath
	b.controlConfig = controlConfig
	b.controlDir = dir
	b.ephemeralDir = ephemeralDir
	b.running = true
	b.forwarded = false
	b.exit = nil
	done := make(chan struct{})
	b.done = done
	b.mu.Unlock()

	go b.reap(cmd, startedAt, controlPath, controlConfig, dir, ephemeralDir, buf, done)
	return nil
}

func (b *Backend) reap(cmd *exec.Cmd, startedAt time.Time, controlPath, controlConfig, controlDir string, ephemeralDir bool, buf *tailBuffer, done chan struct{}) {
	err := cmd.Wait()
	code := -1
	if err == nil {
		code = 0
	} else if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	b.mu.Lock()
	if b.cmd == cmd {
		b.running = false
		b.forwarded = false
		b.exit = &ExitInfo{Code: code, StderrTail: strings.TrimSpace(buf.Tail()), Uptime: time.Since(startedAt), processExit: true}
	}
	b.mu.Unlock()
	cleanupControlArtifacts(controlPath, controlConfig, controlDir, ephemeralDir)
	close(done)
}

func (b *Backend) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

func (b *Backend) Forwarded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running && b.forwarded
}

func (b *Backend) LastExit() *ExitInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exit == nil {
		return nil
	}
	cp := *b.exit
	return &cp
}

func (b *Backend) ConsumeLastExit() *ExitInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exit == nil {
		return nil
	}
	exit := b.exit
	b.exit = nil
	cp := *exit
	return &cp
}

func (b *Backend) ControlPath() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.controlPath
}

// Done returns a channel that closes when the currently owned SSH child exits.
// A supervisor can wait on it alongside its probe timer so a dead child
// invalidates a previously healthy state immediately instead of remaining
// hidden until the next periodic probe.
func (b *Backend) Done() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.done
}

// WaitReady waits until `ssh -O check` proves that the spawned master owns its
// private socket. A running PID alone is not treated as readiness.
func (b *Backend) WaitReady(ctx context.Context, timeout time.Duration) error {
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if !b.Running() {
			if exit := b.LastExit(); exit != nil {
				return fmt.Errorf("ssh master exited with code %d", exit.Code)
			}
			return fmt.Errorf("ssh master exited before control socket was ready")
		}
		if err := b.Check(readyCtx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("wait for ssh control master: %w (last check: %v)", readyCtx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (b *Backend) Check(ctx context.Context) error {
	_, err := b.runControl(ctx, "check")
	return err
}

// Forward adds the sole loopback-bound reverse forward through the already
// running master. It never relies on -R surviving ClearAllForwardings.
func (b *Backend) Forward(ctx context.Context) error {
	forward := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", b.Spec.Port, b.Spec.Port)
	out, err := b.runControl(ctx, "forward", "-R", forward)
	if err != nil {
		msg := strings.ToLower(out + " " + err.Error())
		if strings.Contains(msg, "remote port forwarding failed") ||
			strings.Contains(msg, "cannot listen to port") ||
			strings.Contains(msg, "port forwarding failed") {
			return ErrPortConflict
		}
		return fmt.Errorf("forward request failed: %w", err)
	}
	b.mu.Lock()
	b.forwarded = true
	b.mu.Unlock()
	return nil
}

func (b *Backend) cancel(ctx context.Context) error {
	if !b.Forwarded() {
		return nil
	}
	forward := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", b.Spec.Port, b.Spec.Port)
	_, err := b.runControl(ctx, "cancel", "-R", forward)
	if err == nil {
		b.mu.Lock()
		b.forwarded = false
		b.mu.Unlock()
	}
	return err
}

// Stop performs bounded control-socket shutdown before signalling only the
// exact child spawned by this backend.
func (b *Backend) Stop(grace time.Duration) {
	b.mu.Lock()
	cmd := b.cmd
	done := b.done
	running := b.running
	controlPath := b.controlPath
	controlConfig := b.controlConfig
	controlDir := b.controlDir
	ephemeralDir := b.ephemeralDir
	b.mu.Unlock()

	if cmd != nil && running {
		controlOK := true
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := b.cancel(cancelCtx); err != nil {
			controlOK = false
		}
		cancel()
		if controlOK {
			exitCtx, exitCancel := context.WithTimeout(context.Background(), time.Second)
			if _, err := b.runControl(exitCtx, "exit"); err != nil {
				controlOK = false
			}
			exitCancel()
		}

		if controlOK {
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				controlOK = false
			}
		}
		if !controlOK {
			if cmd.Process != nil {
				_ = terminateProcess(cmd.Process)
			}
			select {
			case <-done:
			case <-time.After(grace):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
			}
		}
	}
	b.mu.Lock()
	if b.cmd == cmd {
		b.cmd = nil
		b.done = nil
		b.running = false
		b.forwarded = false
		b.controlDir = ""
		b.ephemeralDir = false
	}
	b.mu.Unlock()
	cleanupControlArtifacts(controlPath, controlConfig, controlDir, ephemeralDir)
}

func (b *Backend) ProbeRemote(ctx context.Context, timeout time.Duration) (tunnel.RemoteTunnelState, error) {
	out, err := b.runRemote(ctx, timeout, tunnel.RemoteHealthProbeCommand(b.Spec.Port))
	state := tunnel.ClassifyRemoteProbeOutput(out)
	if err != nil {
		return state, fmt.Errorf("remote health probe over control socket: %w", err)
	}
	return state, nil
}

func (b *Backend) ProbeIdentity(ctx context.Context, timeout time.Duration) (tunnel.RemoteIdentityState, tunnel.IdentityInfo, error) {
	out, err := b.runRemote(ctx, timeout, tunnel.RemoteIdentityProbeCommand(b.Spec.Port))
	state, identity := tunnel.ClassifyRemoteIdentityProbeOutput(out)
	if err != nil {
		return state, identity, fmt.Errorf("remote identity probe over control socket: %w", err)
	}
	return state, identity, nil
}

func (b *Backend) runRemote(ctx context.Context, timeout time.Duration, remoteCmd string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := b.Check(probeCtx); err != nil {
		return "", fmt.Errorf("check ssh master: %w", err)
	}
	b.mu.Lock()
	controlPath, controlConfig := b.controlPath, b.controlConfig
	b.mu.Unlock()
	cmd := exec.CommandContext(probeCtx, b.sshBinary(), b.probeArgs(controlConfig, controlPath, remoteCmd)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func (b *Backend) runControl(ctx context.Context, operation string, extra ...string) (string, error) {
	b.mu.Lock()
	controlPath, controlConfig := b.controlPath, b.controlConfig
	b.mu.Unlock()
	if controlPath == "" || controlConfig == "" {
		return "", fmt.Errorf("managed ssh master not started")
	}
	args := b.controlArgs(controlConfig, controlPath, operation, extra...)
	cmd := exec.CommandContext(ctx, b.sshBinary(), args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// MasterArgs starts a private master with no inherited or command-line
// forwarding. The user's SSH config is still read to resolve the named host.
func (b *Backend) MasterArgs(controlPath string) []string {
	return []string{
		"-M", "-N", "-T", "-S", controlPath,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ForkAfterAuthentication=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ControlMaster=yes",
		"-o", "ControlPersist=no",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"--", b.Spec.Host,
	}
}

func (b *Backend) controlArgs(config, controlPath, operation string, extra ...string) []string {
	args := []string{"-F", config, "-S", controlPath, "-o", "BatchMode=yes", "-O", operation}
	args = append(args, extra...)
	return append(args, "--", b.Spec.Host)
}

func (b *Backend) probeArgs(config, controlPath, remoteCmd string) []string {
	return []string{
		"-F", config,
		"-S", controlPath,
		"-o", "BatchMode=yes",
		"-o", "ClearAllForwardings=yes",
		"--", b.Spec.Host, shim.WrapRemoteShell(remoteCmd),
	}
}

func (b *Backend) sshBinary() string {
	if b.SSHBinary != "" {
		return b.SSHBinary
	}
	return "ssh"
}

func (b *Backend) prepareControlDir() (string, bool, error) {
	if b.ControlDir != "" {
		if err := ensurePrivateDir(b.ControlDir); err != nil {
			return "", false, err
		}
		return b.ControlDir, false, nil
	}
	return newDefaultControlDir()
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("create control socket dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect control socket dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("control socket path %s is not a directory", dir)
	}
	if !controlDirOwnedByCurrentUser(info) {
		return fmt.Errorf("control socket dir %s is owned by another user", dir)
	}
	if err := os.Chmod(dir, stateDirMode); err != nil {
		return fmt.Errorf("secure control socket dir: %w", err)
	}
	return nil
}

func cleanupControlArtifacts(controlPath, controlConfig, controlDir string, ephemeralDir bool) {
	if controlPath != "" {
		_ = os.Remove(controlPath)
	}
	if controlConfig != "" {
		_ = os.Remove(controlConfig)
	}
	if ephemeralDir && controlDir != "" {
		_ = os.Remove(controlDir)
	}
}

func newControlSocketPath(dir string) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("generate control socket name: %w", err)
	}
	return filepath.Join(dir, "ctl-"+hex.EncodeToString(rnd[:])+".sock"), nil
}

func newEmptyControlConfig(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stateFileMode)
	if err != nil {
		return "", fmt.Errorf("create empty control config: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close empty control config: %w", err)
	}
	return path, nil
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) Tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
