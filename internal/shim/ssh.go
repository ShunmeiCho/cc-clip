package shim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// SessionExecutor abstracts a remote SSH session for install/uninstall/
// detect/recover testing. *SSHSession satisfies this interface; tests use
// a localSession stub that runs commands locally against a temp $HOME.
type SessionExecutor interface {
	// Exec runs a remote shell command and returns STDOUT only.
	// Stderr is intentionally discarded to match the existing semantics of
	// (*SSHSession).Exec — SSH multiplex chatter (control socket noise,
	// mux_client_forward, etc.) lands on stderr and would otherwise pollute
	// callers that grep the output for content markers.
	Exec(cmd string) (stdout string, err error)

	// ExecWithStdin runs a remote shell command with the provided stdin
	// and returns COMBINED stdout+stderr. Combined output is appropriate
	// here because this method is used during install/uninstall where
	// stderr diagnostics (e.g. "mv: cannot create regular file: Permission
	// denied") are required for actionable error messages.
	ExecWithStdin(cmd string, stdin io.Reader) (combinedOutput string, err error)
}

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

// ExecWithStdin runs a remote shell command with the provided stdin via
// the SSH master connection. Returns combined stdout+stderr output for
// install/uninstall error diagnostics.
func (s *SSHSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
	args := append(s.connArgs(), s.host, cmd)
	c := exec.Command("ssh", args...)
	c.Stdin = stdin
	out, err := c.CombinedOutput()
	return string(out), err
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

// InstallRemoteClaudeWrapper installs the claude wrapper to ~/.local/bin/claude
// on the remote, using a symlink-safe topology that never overwrites the real
// claude binary even when ~/.local/bin/claude is a symlink to a versions store
// (Anthropic Native Installer layout, see issue #55).
//
// Topology:
//   - The original entry (regular file or symlink) is renamed to
//     ~/.local/bin/claude.cc-clip-real.
//   - The wrapper script is written via mktemp + chmod + atomic mv.
//   - On re-install over an existing cc-clip wrapper, the wrapper file is
//     replaced atomically; the existing sidecar is left untouched.
func InstallRemoteClaudeWrapper(s SessionExecutor, port int) error {
	kind, err := classifyClaudeBin(s)
	if err != nil {
		return fmt.Errorf("install: classify failed: %w", err)
	}
	switch kind {
	case claudeBinNone:
		return installWrapperNoOrigin(s, port)
	case claudeBinCcWrapper:
		return installWrapperOverwriteSelf(s, port)
	case claudeBinRegular, claudeBinSymlink:
		return installWrapperWithSidecar(s, port)
	default:
		return fmt.Errorf("install: unsupported origin kind %q", kind)
	}
}

// installWrapperNoOrigin writes the wrapper to a fresh ~/.local/bin/claude
// when no prior file exists. No sidecar is created.
func installWrapperNoOrigin(s SessionExecutor, port int) error {
	script := ClaudeWrapperScript(port)
	cmd := `mkdir -p "$HOME/.local/bin" && \
tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX") && \
cat > "$tmp" && \
chmod +x "$tmp" && \
mv "$tmp" "$HOME/.local/bin/claude"`
	out, err := s.ExecWithStdin(cmd, strings.NewReader(script))
	if err != nil {
		return fmt.Errorf("install (none): %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

// installWrapperWithSidecar handles the regular-file and symlink origin kinds:
// stage the origin to ~/.local/bin/claude.cc-clip-real, then commit the wrapper.
// Uses prepare-then-commit ordering so failures before the final mv leave the
// user's claude untouched, and failures after staging trigger a best-effort
// rollback that restores the origin.
//
// Pre-flight refuses if the sidecar path already exists (regular file, symlink,
// or directory). The user must remove a stale sidecar manually before re-running.
// Refusing rather than overwriting avoids the silent-corruption footgun where
// the sidecar path is a directory and `mv claude .cc-clip-real` would move
// claude INTO the directory rather than rename to it.
func installWrapperWithSidecar(s SessionExecutor, port int) error {
	// Pre-flight: refuse if sidecar already exists.
	out, _ := s.Exec(`if test -e "$HOME/.local/bin/claude.cc-clip-real" || test -L "$HOME/.local/bin/claude.cc-clip-real"; then echo conflict; fi`)
	if strings.TrimSpace(out) == "conflict" {
		return fmt.Errorf("install: ~/.local/bin/claude.cc-clip-real already exists; remove it manually and re-run")
	}

	script := ClaudeWrapperScript(port)
	cmd := `set -e
mkdir -p "$HOME/.local/bin"
tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod +x "$tmp"
# Stage origin to sidecar (mv on a symlink renames the link itself,
# never reads/writes through it).
mv "$HOME/.local/bin/claude" "$HOME/.local/bin/claude.cc-clip-real"
# Commit wrapper.
if ! mv "$tmp" "$HOME/.local/bin/claude"; then
    # Best-effort rollback: restore origin so user's claude keeps working.
    mv "$HOME/.local/bin/claude.cc-clip-real" "$HOME/.local/bin/claude" 2>/dev/null || true
    exit 1
fi
trap - EXIT  # tmp now consumed by the mv`
	outErr, err := s.ExecWithStdin(cmd, strings.NewReader(script))
	if err != nil {
		return fmt.Errorf("install (with-sidecar): %s: %w", strings.TrimSpace(outErr), err)
	}
	return nil
}

// installWrapperOverwriteSelf replaces our own previous wrapper at
// ~/.local/bin/claude via mktemp + atomic mv. The sidecar is left untouched
// because we already own the claude path (it's our wrapper, not user state).
func installWrapperOverwriteSelf(s SessionExecutor, port int) error {
	script := ClaudeWrapperScript(port)
	cmd := `set -e
mkdir -p "$HOME/.local/bin"
tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod +x "$tmp"
mv "$tmp" "$HOME/.local/bin/claude"
trap - EXIT`
	out, err := s.ExecWithStdin(cmd, strings.NewReader(script))
	if err != nil {
		return fmt.Errorf("install (cc_wrapper overwrite): %s: %w", strings.TrimSpace(out), err)
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
// If the user already has a non-managed `notify` key, injection is refused
// to avoid creating duplicate TOML keys.
func EnsureRemoteCodexNotifyConfig(session *SSHSession, port int) error {
	const markerStart = "# >>> cc-clip notify (do not edit) >>>"
	const markerEnd = "# <<< cc-clip notify (do not edit) <<<"
	const configPath = "~/.codex/config.toml"

	managedBlock := codexNotifyManagedBlock(markerStart, markerEnd, port)

	// Check if the managed block already exists.
	out, _ := session.Exec(fmt.Sprintf("grep -F %q %s 2>/dev/null || true", markerStart, configPath))
	if strings.Contains(out, markerStart) {
		// Replace existing block using sed.
		sedCmd := fmt.Sprintf(
			`sed -i.cc-clip-bak '/%s/,/%s/d' %s 2>/dev/null; rm -f %s.cc-clip-bak`,
			sedEscape(markerStart), sedEscape(markerEnd), configPath, configPath)
		session.Exec(sedCmd)
	} else {
		// Check for a user-managed notify key (not ours) to avoid duplicate keys.
		userNotify, _ := session.Exec(fmt.Sprintf(
			"grep -E '^\\s*notify\\s*=' %s 2>/dev/null || true", configPath))
		if strings.TrimSpace(userNotify) != "" {
			return fmt.Errorf("existing notify setting found in %s — refusing to inject duplicate. Remove or comment out the existing notify line first", configPath)
		}
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

func codexNotifyManagedBlock(markerStart, markerEnd string, port int) string {
	// Include port in CC_CLIP_PORT env so non-default ports work.
	if port == 18339 {
		return markerStart + "\n" +
			`notify = ["cc-clip", "notify", "--from-codex-stdin"]` + "\n" +
			markerEnd
	}
	return markerStart + "\n" +
		fmt.Sprintf(`notify = ["env", "CC_CLIP_PORT=%d", "cc-clip", "notify", "--from-codex-stdin"]`, port) + "\n" +
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

// claudeBinKind values returned by classifyClaudeBin. Using named constants
// (rather than bare strings) so the switch statements in install/uninstall
// fail at compile time if a typo or rename is ever introduced.
const (
	claudeBinNone      = "none"
	claudeBinCcWrapper = "cc_wrapper"
	claudeBinSymlink   = "symlink"
	claudeBinRegular   = "regular"
	claudeBinOther     = "other"
)

// classifyClaudeBin inspects ~/.local/bin/claude on the remote and returns
// one of: "none", "cc_wrapper", "symlink", "regular", "other".
//
// "cc_wrapper" means a regular file whose first 256 bytes contain the
// cc-clip wrapper marker — i.e. our previous install. Used to skip the
// sidecar staging step on re-install (we just overwrite our own wrapper).
//
// Inspection is read-only; nothing is modified.
func classifyClaudeBin(s SessionExecutor) (string, error) {
	out, err := s.Exec(`p="$HOME/.local/bin/claude"
if [ ! -e "$p" ] && [ ! -L "$p" ]; then echo none; exit 0; fi
if [ -L "$p" ]; then echo symlink; exit 0; fi
if [ -f "$p" ]; then
    if head -c 256 "$p" 2>/dev/null | grep -qF "# cc-clip claude wrapper"; then
        echo cc_wrapper
    else
        echo regular
    fi
    exit 0
fi
echo other`)
	if err != nil {
		return "", fmt.Errorf("classify ~/.local/bin/claude: %w", err)
	}
	return strings.TrimSpace(out), nil
}
