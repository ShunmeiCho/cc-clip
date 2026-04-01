package shim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// SSHSession manages a persistent SSH ControlMaster connection for reuse
// across multiple remote operations, avoiding repeated passphrase prompts.
type SSHSession struct {
	host        string
	controlPath string
}

// NewSSHSession starts an SSH ControlMaster session to the given host.
// All subsequent Exec/Upload calls reuse this connection.
// The caller must call Close() when done (typically via defer).
func NewSSHSession(host string) (*SSHSession, error) {
	// Windows OpenSSH does not support ControlMaster.
	// Run each SSH command independently (relies on ssh-agent for auth).
	if runtime.GOOS == "windows" {
		return &SSHSession{
			host:        host,
			controlPath: "",
		}, nil
	}

	// Create a temp file path for the control socket.
	// We cannot use /tmp/cc-clip-ssh-%C because %C is expanded by ssh,
	// but we want a unique, predictable path. Let ssh expand %C itself.
	controlPath := "/tmp/cc-clip-ssh-%C"

	cmd := exec.Command("ssh",
		"-fN",
		"-o", "ControlMaster=yes",
		"-o", fmt.Sprintf("ControlPath=%s", controlPath),
		"-o", "ControlPersist=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ClearAllForwardings=yes",
		host,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to start SSH master connection to %s: %w", host, err)
	}

	return &SSHSession{
		host:        host,
		controlPath: controlPath,
	}, nil
}

// NewSSHSessionWithControlPath creates an SSHSession with a specific control path.
// This is primarily useful for testing.
func NewSSHSessionWithControlPath(host, controlPath string) (*SSHSession, error) {
	cmd := exec.Command("ssh",
		"-fN",
		"-o", "ControlMaster=yes",
		"-o", fmt.Sprintf("ControlPath=%s", controlPath),
		"-o", "ControlPersist=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ClearAllForwardings=yes",
		host,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to start SSH master connection to %s: %w", host, err)
	}

	return &SSHSession{
		host:        host,
		controlPath: controlPath,
	}, nil
}

// connArgs returns the SSH connection arguments for this session.
// With ControlMaster: uses ControlPath. Without (Windows): uses ClearAllForwardings
// to prevent user's RemoteForward from triggering on every independent invocation.
func (s *SSHSession) connArgs() []string {
	if s.controlPath != "" {
		return []string{"-o", fmt.Sprintf("ControlPath=%s", s.controlPath)}
	}
	return []string{"-o", "ClearAllForwardings=yes"}
}

// Exec runs a command on the remote host via the SSH master connection.
// Only stdout is captured as the return value; stderr is discarded to avoid
// SSH mux control messages (e.g. "mux_client_forward:") contaminating output.
func (s *SSHSession) Exec(cmd string) (string, error) {
	args := append(s.connArgs(), s.host, cmd)
	c := exec.Command("ssh", args...)
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// Upload copies a local file to the remote host via the SSH master connection.
func (s *SSHSession) Upload(localPath, remotePath string) error {
	scpArgs := append(s.connArgs(), localPath, fmt.Sprintf("%s:%s", s.host, remotePath))
	cmd := exec.Command("scp", scpArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Make the uploaded file executable
	chmodArgs := append(s.connArgs(), s.host, fmt.Sprintf("chmod +x %s", remotePath))
	chmodCmd := exec.Command("ssh", chmodArgs...)
	if out, err := chmodCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chmod failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// Close terminates the SSH master connection.
func (s *SSHSession) Close() error {
	if s.controlPath == "" {
		return nil // No ControlMaster on Windows
	}
	cmd := exec.Command("ssh",
		"-O", "exit",
		"-o", fmt.Sprintf("ControlPath=%s", s.controlPath),
		s.host,
	)
	// Ignore errors on close — master may have already exited
	_ = cmd.Run()
	return nil
}

// Host returns the remote host this session is connected to.
func (s *SSHSession) Host() string {
	return s.host
}

// ControlPath returns the control socket path for this session.
func (s *SSHSession) ControlPath() string {
	return s.controlPath
}

// --- Session-aware variants of existing functions ---

// DetectRemoteArchViaSession detects the remote OS/arch using an existing SSH session.
func DetectRemoteArchViaSession(session *SSHSession) (string, string, error) {
	out, err := session.Exec("uname -sm")
	if err != nil {
		return "", "", fmt.Errorf("failed to detect remote arch: %w", err)
	}

	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) < 2 {
		return "", "", fmt.Errorf("unexpected uname output: %s", out)
	}

	goos := strings.ToLower(parts[0])
	arch := parts[1]

	goarch := ""
	switch arch {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	default:
		goarch = arch
	}

	return goos, goarch, nil
}

// UploadBinaryViaSession uploads a binary using an existing SSH session.
func UploadBinaryViaSession(session *SSHSession, localBin, remoteBin string) error {
	return session.Upload(localBin, remoteBin)
}

// RemoteExecViaSession runs a remote command using an existing SSH session.
func RemoteExecViaSession(session *SSHSession, args ...string) (string, error) {
	cmdStr := strings.Join(args, " ")
	return session.Exec(cmdStr)
}

// WriteRemoteTokenViaSession writes the session token to the remote host
// via the SSH master connection, using stdin to avoid exposing the token
// in process arguments or shell history.
func WriteRemoteTokenViaSession(session *SSHSession, tok string) error {
	args := append(session.connArgs(), session.host,
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.token && chmod 600 ~/.cache/cc-clip/session.token")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(tok + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write remote token: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// GenerateSessionID creates a random session identifier for transfer tracking.
func GenerateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// GenerateNotificationNonce creates a random nonce for notification auth.
// Returns 32 random bytes encoded as a 64-character hex string.
// This is intentionally longer than GenerateSessionID (16 bytes) to
// ensure the two cannot be confused or accidentally swapped.
func GenerateNotificationNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate notification nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// WriteRemoteNotificationNonce writes the notification nonce to
// ~/.cache/cc-clip/notify.nonce on the remote with chmod 600.
func WriteRemoteNotificationNonce(session *SSHSession, nonce string) error {
	args := append(session.connArgs(), session.host,
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/notify.nonce && chmod 600 ~/.cache/cc-clip/notify.nonce")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(nonce + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write remote notification nonce: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// InstallRemoteHookScript writes the cc-clip-hook bash script to
// ~/.local/bin/cc-clip-hook on the remote with chmod +x.
func InstallRemoteHookScript(session *SSHSession, port int) error {
	script := HookScript(port)
	args := append(session.connArgs(), session.host,
		"mkdir -p ~/.local/bin && cat > ~/.local/bin/cc-clip-hook && chmod +x ~/.local/bin/cc-clip-hook")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install remote hook script: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// RemoteHasCodex checks whether ~/.codex directory exists on the remote.
func RemoteHasCodex(session *SSHSession) bool {
	_, err := session.Exec("test -d ~/.codex")
	return err == nil
}

// EnsureRemoteCodexNotifyConfig injects the cc-clip notification hook
// block into ~/.codex/config.toml using # cc-clip-managed guard markers.
// Idempotent: if the managed block already exists, it is replaced.
func EnsureRemoteCodexNotifyConfig(session *SSHSession) error {
	const markerStart = "# >>> cc-clip notify (do not edit) >>>"
	const markerEnd = "# <<< cc-clip notify (do not edit) <<<"
	const configPath = "~/.codex/config.toml"

	managedBlock := codexNotifyManagedBlock(markerStart, markerEnd)

	// Check if the managed block already exists.
	out, _ := session.Exec(fmt.Sprintf("grep -F %q %s 2>/dev/null || true", markerStart, configPath))
	if strings.Contains(out, markerStart) {
		// Replace existing block using sed.
		sedCmd := fmt.Sprintf(
			`sed -i.cc-clip-bak '/%s/,/%s/d' %s 2>/dev/null; rm -f %s.cc-clip-bak`,
			sedEscape(markerStart), sedEscape(markerEnd), configPath, configPath)
		session.Exec(sedCmd)
	}

	// Append the managed block to the config file.
	appendCmd := fmt.Sprintf(
		"mkdir -p ~/.codex && cat >> %s << 'CC_CLIP_EOF'\n%s\nCC_CLIP_EOF",
		configPath, managedBlock)
	_, err := session.Exec(appendCmd)
	if err != nil {
		return fmt.Errorf("failed to inject notify config into %s: %w", configPath, err)
	}

	return nil
}

func codexNotifyManagedBlock(markerStart, markerEnd string) string {
	// Codex notify is configured as a command array in config.toml. The payload
	// JSON is provided on stdin, so the managed command reads from stdin instead
	// of relying on shell interpolation.
	return markerStart + "\n" +
		`notify = ["cc-clip", "notify", "--from-codex-stdin"]` + "\n" +
		markerEnd
}

// WriteRemoteSessionID writes a session ID to ~/.cache/cc-clip/session.id on the remote.
func WriteRemoteSessionID(session *SSHSession, sessionID string) error {
	args := append(session.connArgs(), session.host,
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.id && chmod 600 ~/.cache/cc-clip/session.id")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(sessionID + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write remote session ID: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
