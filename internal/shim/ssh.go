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

	cmd := exec.Command("ssh", sshHostArgs([]string{
		"-fN",
		"-o", "ControlMaster=yes",
		"-o", fmt.Sprintf("ControlPath=%s", controlPath),
		"-o", "ControlPersist=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ClearAllForwardings=yes",
	}, host)...)
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
	cmd := exec.Command("ssh", sshHostArgs([]string{
		"-fN",
		"-o", "ControlMaster=yes",
		"-o", fmt.Sprintf("ControlPath=%s", controlPath),
		"-o", "ControlPersist=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ClearAllForwardings=yes",
	}, host)...)
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

func sshHostArgs(prefix []string, host string, remoteArgs ...string) []string {
	args := append([]string{}, prefix...)
	args = append(args, "--", host)
	args = append(args, remoteArgs...)
	return args
}

func scpUploadArgs(prefix []string, localPath, host, remotePath string) []string {
	args := append([]string{}, prefix...)
	args = append(args, "--", localPath, fmt.Sprintf("%s:%s", host, remotePath))
	return args
}

func (s *SSHSession) sshArgs(remoteArgs ...string) []string {
	return sshHostArgs(s.connArgs(), s.host, remoteArgs...)
}

// Exec runs a command on the remote host via the SSH master connection.
// Only stdout is captured as the return value; stderr is discarded to avoid
// SSH mux control messages (e.g. "mux_client_forward:") contaminating output.
func (s *SSHSession) Exec(cmd string) (string, error) {
	c := exec.Command("ssh", s.sshArgs(cmd)...)
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// Upload copies a local file to the remote host via the SSH master connection.
func (s *SSHSession) Upload(localPath, remotePath string) error {
	cmd := exec.Command("scp", scpUploadArgs(s.connArgs(), localPath, s.host, remotePath)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Make the uploaded file executable
	chmodCmd := exec.Command("ssh", s.sshArgs(fmt.Sprintf("chmod +x %s", remotePath))...)
	if out, err := chmodCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chmod failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// ExecWithStdin runs a remote shell command with the provided stdin via
// the SSH master connection. Returns combined stdout+stderr output for
// install/uninstall error diagnostics.
func (s *SSHSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
	c := exec.Command("ssh", s.sshArgs(cmd)...)
	c.Stdin = stdin
	out, err := c.CombinedOutput()
	return string(out), err
}

// Close terminates the SSH master connection.
func (s *SSHSession) Close() error {
	if s.controlPath == "" {
		return nil // No ControlMaster on Windows
	}
	cmd := exec.Command("ssh", sshHostArgs([]string{
		"-O", "exit",
		"-o", fmt.Sprintf("ControlPath=%s", s.controlPath),
	}, s.host)...)
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
	cmd := exec.Command("ssh", session.sshArgs(
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.token && chmod 600 ~/.cache/cc-clip/session.token")...)
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
	cmd := exec.Command("ssh", session.sshArgs(
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/notify.nonce && chmod 600 ~/.cache/cc-clip/notify.nonce")...)
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
	cmd := exec.Command("ssh", session.sshArgs(
		"mkdir -p ~/.local/bin && cat > ~/.local/bin/cc-clip-hook && chmod +x ~/.local/bin/cc-clip-hook")...)
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
func RemoteHasCodex(session RemoteExecutor) (bool, error) {
	out, err := session.Exec("if [ -d ~/.codex ]; then echo yes; else echo no; fi")
	if err != nil {
		return false, fmt.Errorf("failed to check remote Codex config dir: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected Codex config dir probe output: %q", out)
	}
}

// EnsureRemoteCodexNotifyConfig injects the cc-clip notification hook
// block into ~/.codex/config.toml using # cc-clip-managed guard markers.
// Idempotent: if the managed block already exists, it is replaced.
// If the user already has a non-managed `notify` key, injection is refused
// to avoid creating duplicate TOML keys.
func EnsureRemoteCodexNotifyConfig(session RemoteExecutor, port int) error {
	const markerStart = "# >>> cc-clip notify (do not edit) >>>"
	const markerEnd = "# <<< cc-clip notify (do not edit) <<<"
	const configPath = "~/.codex/config.toml"

	managedBlock := codexNotifyManagedBlock(markerStart, markerEnd, port)

	// Check if the managed block already exists.
	out, err := remoteOptionalGrep(session, "-F", markerStart, configPath)
	if err != nil {
		return fmt.Errorf("failed to check managed notify block in %s: %w", configPath, err)
	}
	if strings.Contains(out, markerStart) {
		// Replace existing block using sed.
		sedCmd := fmt.Sprintf(
			`sed -i.cc-clip-bak '/%s/,/%s/d' %s && rm -f %s.cc-clip-bak`,
			sedEscape(markerStart), sedEscape(markerEnd), configPath, configPath)
		if _, err := session.Exec(sedCmd); err != nil {
			return fmt.Errorf("failed to replace existing managed notify block in %s: %w", configPath, err)
		}
	} else {
		// Check for a user-managed notify key (not ours) to avoid duplicate keys.
		userNotify, err := remoteOptionalGrep(session, "-E", `^[[:space:]]*notify[[:space:]]*=`, configPath)
		if err != nil {
			return fmt.Errorf("failed to check existing notify setting in %s: %w", configPath, err)
		}
		if strings.TrimSpace(userNotify) != "" {
			return fmt.Errorf("existing notify setting found in %s — refusing to inject duplicate. Remove or comment out the existing notify line first", configPath)
		}
	}

	// Append the managed block to the config file.
	appendCmd := fmt.Sprintf(
		"mkdir -p ~/.codex && cat >> %s << 'CC_CLIP_EOF'\n%s\nCC_CLIP_EOF",
		configPath, managedBlock)
	_, err = session.Exec(appendCmd)
	if err != nil {
		return fmt.Errorf("failed to inject notify config into %s: %w", configPath, err)
	}

	return nil
}

func remoteOptionalGrep(session RemoteExecutor, flag, pattern, path string) (string, error) {
	cmd := fmt.Sprintf(`if [ -e %s ]; then grep %s %q %s; status=$?; case "$status" in 0|1) exit 0;; *) exit "$status";; esac; fi`, path, flag, pattern, path)
	out, err := session.Exec(cmd)
	if err != nil {
		return "", err
	}
	return out, nil
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
	cmd := exec.Command("ssh", session.sshArgs(
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.id && chmod 600 ~/.cache/cc-clip/session.id")...)
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

// RecoverV070Corruption migrates the v0.7.0 backup file (which contains the
// real claude binary) back to the symlink's target path, restoring the
// original Native Installer layout. After this call, the caller is expected
// to re-run InstallRemoteClaudeWrapper to install a v0.7.1+ wrapper safely.
//
// This function re-verifies all five corruption conditions inside the same
// SSH session before doing anything destructive (TOCTOU guard).
func RecoverV070Corruption(s SessionExecutor) error {
	corrupted, err := DetectV070Corruption(s)
	if err != nil {
		return fmt.Errorf("recover: re-detection failed: %w", err)
	}
	if !corrupted {
		return fmt.Errorf("recover: corruption no longer detected; refusing to migrate backup")
	}
	out, err := s.Exec(`set -e
target=$(readlink -f "$HOME/.local/bin/claude")
mv "$HOME/.local/bin/claude.cc-clip-bak" "$target"`)
	if err != nil {
		return fmt.Errorf("recover: migrate backup: %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

// UninstallRemoteClaudeWrapper removes the cc-clip claude wrapper from
// ~/.local/bin/claude and restores the origin from ~/.local/bin/claude.cc-clip-real.
//
// Safety: refuses if ~/.local/bin/claude is not a cc-clip wrapper (i.e. the
// user replaced it with their own file). In that case, returns an error
// without touching anything.
//
// If a v0.7.0-era ~/.local/bin/claude.cc-clip-bak file exists, it is left
// in place — recovery of that backup is the user's call (see
// RecoverV070Corruption + --auto-recover).
func UninstallRemoteClaudeWrapper(s SessionExecutor) error {
	// Verify file at claude is a cc-clip wrapper (and exists).
	out, err := s.Exec(`p="$HOME/.local/bin/claude"
if [ ! -e "$p" ] && [ ! -L "$p" ]; then echo absent; exit 0; fi
if [ -L "$p" ] || [ ! -f "$p" ]; then echo notwrapper; exit 0; fi
if head -c 256 "$p" 2>/dev/null | grep -qF "# cc-clip claude wrapper"; then
    echo wrapper
else
    echo notwrapper
fi`)
	if err != nil {
		return fmt.Errorf("uninstall: classify failed: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "absent":
		return nil
	case "notwrapper":
		return fmt.Errorf("uninstall: ~/.local/bin/claude is not a cc-clip wrapper; refusing to remove")
	case "wrapper":
		// proceed
	default:
		return fmt.Errorf("uninstall: unexpected classify output %q", strings.TrimSpace(out))
	}

	// Remove wrapper, restore origin from sidecar if present.
	cmd := `set -e
rm "$HOME/.local/bin/claude"
if [ -e "$HOME/.local/bin/claude.cc-clip-real" ] || [ -L "$HOME/.local/bin/claude.cc-clip-real" ]; then
    mv "$HOME/.local/bin/claude.cc-clip-real" "$HOME/.local/bin/claude"
fi`
	outErr, err := s.Exec(cmd)
	if err != nil {
		return fmt.Errorf("uninstall: %s: %w", strings.TrimSpace(outErr), err)
	}
	return nil
}

// V070State classifies the v0.7.0 wrapper-install bug aftermath into three
// outcomes that the CLI N0 gate must distinguish:
//
//	V070NotCorrupted   : safe to proceed with install; either there is no
//	                     symlink at ~/.local/bin/claude, the symlink target
//	                     is not a cc-clip wrapper, or the target file does
//	                     not exist (broken symlink) — i.e. C1 or C2 failed.
//	V070Recoverable    : C1..C5 all hold; --auto-recover can migrate the
//	                     backup at ~/.local/bin/claude.cc-clip-bak back to
//	                     the symlink target.
//	V070NonRecoverable : C1+C2 hold (~/.local/bin/claude IS a symlink whose
//	                     target is a cc-clip wrapper) but the backup file
//	                     is missing, too small, or is itself a wrapper.
//	                     Auto-recovery cannot fix this — the real Native
//	                     Installer binary is lost. Operator must reinstall
//	                     Claude Code via curl https://claude.ai/install.sh
//	                     before re-running cc-clip.
//
// The three-state split closes the fail-open gap that existed when the
// detector only returned a boolean: previously, "target is wrapper but
// backup is missing" collapsed into the same false branch as "no corruption
// at all", and the install path proceeded unconditionally, layering a fresh
// wrapper on top of an already-broken Native Installer layout.
type V070State int

const (
	V070NotCorrupted V070State = iota
	V070Recoverable
	V070NonRecoverable
)

// String returns a stable short name suitable for diagnostic output.
func (s V070State) String() string {
	switch s {
	case V070NotCorrupted:
		return "not_corrupted"
	case V070Recoverable:
		return "recoverable"
	case V070NonRecoverable:
		return "non_recoverable"
	default:
		return "unknown"
	}
}

// DetectV070State classifies the remote into one of three v0.7.0 states
// and returns a stable diag token that pinpoints which condition decided
// the outcome (useful for logs and tests). The check is strictly read-only.
//
// Diag tokens are stable identifiers (not human prose):
//
//	not_corrupted_C1_not_symlink     : ~/.local/bin/claude is not a symlink
//	not_corrupted_C2_target_missing  : symlink target does not exist
//	not_corrupted_C2_not_wrapper     : symlink target is not a cc-clip wrapper
//	recoverable                       : all five conditions hold; auto-fix viable
//	non_recoverable_C3_backup_missing : target IS wrapper but no backup
//	non_recoverable_C4_backup_too_small : backup < 1 MiB (cannot be real binary)
//	non_recoverable_C5_backup_is_wrapper : backup is itself a cc-clip wrapper
//
// Decision boundary: C1 and C2 are "is the install path actually broken?"
// gates. If either fails, we are NOT corrupted — proceed with install. Only
// after C1+C2 hold do C3/C4/C5 distinguish recoverable from non-recoverable.
func DetectV070State(s SessionExecutor) (V070State, string, error) {
	out, err := s.Exec(`set -e
claude="$HOME/.local/bin/claude"
bak="$HOME/.local/bin/claude.cc-clip-bak"
# C1: claude is a symlink.
[ -L "$claude" ] || { echo not_corrupted_C1_not_symlink; exit 0; }
# C2: target exists and contains wrapper marker.
target=$(readlink -f "$claude") || { echo not_corrupted_C2_target_missing; exit 0; }
[ -f "$target" ] || { echo not_corrupted_C2_target_missing; exit 0; }
head -c 256 "$target" 2>/dev/null | grep -qF "# cc-clip claude wrapper" || { echo not_corrupted_C2_not_wrapper; exit 0; }
# At this point C1+C2 hold: target IS a wrapper. Any failure below is non-recoverable.
# C3: backup is a regular file.
[ -f "$bak" ] || { echo non_recoverable_C3_backup_missing; exit 0; }
# C4: backup > 1 MiB.
size=$(wc -c < "$bak" | tr -d ' ')
[ "$size" -gt 1048576 ] || { echo non_recoverable_C4_backup_too_small; exit 0; }
# C5: backup is NOT itself a wrapper.
if head -c 256 "$bak" 2>/dev/null | grep -qF "# cc-clip claude wrapper"; then
    echo non_recoverable_C5_backup_is_wrapper
    exit 0
fi
echo recoverable`)
	if err != nil {
		return V070NotCorrupted, "", fmt.Errorf("detect v0.7.0 state: %w", err)
	}
	diag := strings.TrimSpace(out)
	switch {
	case strings.HasPrefix(diag, "not_corrupted"):
		return V070NotCorrupted, diag, nil
	case diag == "recoverable":
		return V070Recoverable, diag, nil
	case strings.HasPrefix(diag, "non_recoverable"):
		return V070NonRecoverable, diag, nil
	default:
		return V070NotCorrupted, diag, fmt.Errorf("detect v0.7.0 state: unexpected diag %q", diag)
	}
}

// DetectV070Corruption is the boolean compat wrapper kept for RecoverV070Corruption's
// TOCTOU re-check. Returns true only on V070Recoverable — never on V070NonRecoverable,
// because Recover would be unsafe in that state. Use DetectV070State for callers
// that need to distinguish all three outcomes (i.e. the N0 gate).
func DetectV070Corruption(s SessionExecutor) (bool, error) {
	state, _, err := DetectV070State(s)
	if err != nil {
		return false, err
	}
	return state == V070Recoverable, nil
}
