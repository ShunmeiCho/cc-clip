package shim

import (
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

// Exec runs a command on the remote host via the SSH master connection.
// Only stdout is captured as the return value; stderr is discarded to avoid
// SSH mux control messages (e.g. "mux_client_forward:") contaminating output.
func (s *SSHSession) Exec(cmd string) (string, error) {
	args := []string{}
	if s.controlPath != "" {
		args = append(args, "-o", fmt.Sprintf("ControlPath=%s", s.controlPath))
	}
	args = append(args, s.host, cmd)
	c := exec.Command("ssh", args...)
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// Upload copies a local file to the remote host via the SSH master connection.
func (s *SSHSession) Upload(localPath, remotePath string) error {
	scpArgs := []string{}
	if s.controlPath != "" {
		scpArgs = append(scpArgs, "-o", fmt.Sprintf("ControlPath=%s", s.controlPath))
	}
	scpArgs = append(scpArgs, localPath, fmt.Sprintf("%s:%s", s.host, remotePath))
	cmd := exec.Command("scp", scpArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Make the uploaded file executable
	chmodArgs := []string{}
	if s.controlPath != "" {
		chmodArgs = append(chmodArgs, "-o", fmt.Sprintf("ControlPath=%s", s.controlPath))
	}
	chmodArgs = append(chmodArgs, s.host, fmt.Sprintf("chmod +x %s", remotePath))
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
	args := []string{}
	if session.controlPath != "" {
		args = append(args, "-o", fmt.Sprintf("ControlPath=%s", session.controlPath))
	}
	args = append(args, session.host,
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.token && chmod 600 ~/.cache/cc-clip/session.token")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(tok + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write remote token: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
