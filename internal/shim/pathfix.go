package shim

import (
	"fmt"
	"strings"
)

const (
	pathMarkerStart = "# >>> cc-clip PATH (do not edit) >>>"
	pathMarkerEnd   = "# <<< cc-clip PATH (do not edit) <<<"
	pathExport      = `export PATH="$HOME/.local/bin:$PATH"`

	displayMarkerStart = "# >>> cc-clip Codex DISPLAY (do not edit) >>>"
	displayMarkerEnd   = "# <<< cc-clip Codex DISPLAY (do not edit) <<<"
)

// pathBlock returns the full marker block to inject into shell rc files.
func pathBlock() string {
	return pathMarkerStart + "\n" + pathExport + "\n" + pathMarkerEnd + "\n"
}

// RemoteExecutor is the interface that SSHSession (Agent B) can satisfy
// so pathfix functions can work with both RemoteExec and SSHSession.
type RemoteExecutor interface {
	Exec(cmd string) (string, error)
}

// hostExecutor wraps RemoteExec(host, ...) as a RemoteExecutor.
type hostExecutor struct {
	host string
}

func (h *hostExecutor) Exec(cmd string) (string, error) {
	return RemoteExec(h.host, cmd)
}

// DetectRemoteShell runs echo $SHELL on the remote host and returns "bash" or "zsh".
func DetectRemoteShell(host string) (string, error) {
	return DetectRemoteShellSession(&hostExecutor{host: host})
}

// DetectRemoteShellSession detects the remote shell using a RemoteExecutor.
func DetectRemoteShellSession(session RemoteExecutor) (string, error) {
	out, err := session.Exec("echo $SHELL")
	if err != nil {
		return "", fmt.Errorf("failed to detect remote shell: %w", err)
	}

	shell := strings.TrimSpace(out)
	switch {
	case strings.HasSuffix(shell, "/zsh"):
		return "zsh", nil
	case strings.HasSuffix(shell, "/bash"):
		return "bash", nil
	case strings.Contains(shell, "zsh"):
		return "zsh", nil
	case strings.Contains(shell, "bash"):
		return "bash", nil
	default:
		// Default to bash for unknown shells
		return "bash", nil
	}
}

// RCFilePath returns the shell rc file path for the given shell name.
// Returns ~/.bashrc for bash, ~/.zshrc for zsh.
func RCFilePath(shell string) string {
	switch shell {
	case "zsh":
		return "~/.zshrc"
	default:
		return "~/.bashrc"
	}
}

// IsPathFixed checks if the PATH marker block exists in the remote rc file.
func IsPathFixed(host string) (bool, error) {
	return IsPathFixedSession(&hostExecutor{host: host})
}

// IsPathFixedSession checks if the PATH marker block exists using a RemoteExecutor.
func IsPathFixedSession(session RemoteExecutor) (bool, error) {
	shell, err := DetectRemoteShellSession(session)
	if err != nil {
		return false, err
	}

	rcFile := RCFilePath(shell)
	out, err := session.Exec(fmt.Sprintf("grep -F %q %s 2>/dev/null || true", pathMarkerStart, rcFile))
	if err != nil {
		return false, fmt.Errorf("failed to check PATH marker: %w", err)
	}

	return strings.Contains(out, pathMarkerStart), nil
}

// FixRemotePath idempotently injects the PATH marker block into the remote rc file.
func FixRemotePath(host string) error {
	return FixRemotePathSession(&hostExecutor{host: host})
}

// FixRemotePathSession injects the PATH marker block using a RemoteExecutor.
// The block is prepended (not appended) so it takes effect before any
// interactive-only guard like `case $- in *i*) ;; *) return;; esac`.
func FixRemotePathSession(session RemoteExecutor) error {
	fixed, err := IsPathFixedSession(session)
	if err != nil {
		return err
	}
	if fixed {
		return nil
	}

	shell, err := DetectRemoteShellSession(session)
	if err != nil {
		return err
	}

	rcFile := RCFilePath(shell)
	block := pathBlock()

	if err := prependBlock(session, rcFile, block); err != nil {
		return fmt.Errorf("failed to inject PATH block into %s: %w", rcFile, err)
	}

	return nil
}

// RemoveRemotePath removes the PATH marker block from the remote rc file.
func RemoveRemotePath(host string) error {
	return RemoveRemotePathSession(&hostExecutor{host: host})
}

// RemoveRemotePathSession removes the PATH marker block using a RemoteExecutor.
func RemoveRemotePathSession(session RemoteExecutor) error {
	shell, err := DetectRemoteShellSession(session)
	if err != nil {
		return err
	}

	rcFile := RCFilePath(shell)

	// Use sed to remove the marker block (including an optional leading blank line).
	// The pattern matches: optional blank line + marker start + any lines + marker end.
	sedCmd := fmt.Sprintf(
		`sed -i.cc-clip-bak '/%s/,/%s/d' %s 2>/dev/null; rm -f %s.cc-clip-bak`,
		sedEscape(pathMarkerStart),
		sedEscape(pathMarkerEnd),
		rcFile, rcFile)

	_, err = session.Exec(sedCmd)
	if err != nil {
		return fmt.Errorf("failed to remove PATH block from %s: %w", rcFile, err)
	}

	return nil
}

// displayBlock returns the full DISPLAY marker block to inject into shell rc files.
// DISPLAY is set to TCP loopback format (127.0.0.1:{num}) instead of Unix socket
// format (:{num}) because Codex CLI runs inside a sandbox that blocks access to
// the Unix socket at /tmp/.X11-unix/. TCP loopback bypasses this restriction.
func displayBlock() string {
	body := `if [ -z "${DISPLAY:-}" ] && [ -r "$HOME/.cache/cc-clip/codex/display" ]; then
  _cc_clip_display="$(cat "$HOME/.cache/cc-clip/codex/display" 2>/dev/null)"
  case "$_cc_clip_display" in
    :[0-9]*) _cc_clip_num="${_cc_clip_display#:}" ;;
    [0-9]*)  _cc_clip_num="$_cc_clip_display" ;;
    *)       _cc_clip_num="" ;;
  esac
  if [ -n "$_cc_clip_num" ]; then
    export DISPLAY="127.0.0.1:${_cc_clip_num}"
  fi
  unset _cc_clip_display _cc_clip_num
fi`
	return displayMarkerStart + "\n" + body + "\n" + displayMarkerEnd + "\n"
}

// IsDisplayFixedSession checks if the DISPLAY marker block exists in any rc file.
func IsDisplayFixedSession(session RemoteExecutor) (bool, error) {
	for _, rcFile := range []string{"~/.bashrc", "~/.zshrc"} {
		out, _ := session.Exec(fmt.Sprintf("grep -F %q %s 2>/dev/null || true", displayMarkerStart, rcFile))
		if strings.Contains(out, displayMarkerStart) {
			return true, nil
		}
	}
	return false, nil
}

// FixDisplaySession idempotently injects the DISPLAY marker block into shell rc files.
// The block is prepended (not appended) so it takes effect before any
// interactive-only guard like `case $- in *i*) ;; *) return;; esac`.
//
// To handle cases where $SHELL reports bash but the user actually uses zsh
// (or vice versa), the marker is injected into all rc files that exist on
// the remote host.
func FixDisplaySession(session RemoteExecutor) error {
	block := displayBlock()

	rcFiles := activeRCFiles(session)
	if len(rcFiles) == 0 {
		return fmt.Errorf("no shell rc file found on remote")
	}

	for _, rcFile := range rcFiles {
		// Check if already injected in this file.
		out, _ := session.Exec(fmt.Sprintf("grep -F %q %s 2>/dev/null || true", displayMarkerStart, rcFile))
		if strings.Contains(out, displayMarkerStart) {
			continue
		}
		if err := prependBlock(session, rcFile, block); err != nil {
			return fmt.Errorf("failed to inject DISPLAY block into %s: %w", rcFile, err)
		}
	}

	return nil
}

// RemoveDisplayMarkerSession removes the DISPLAY marker block from all rc files.
func RemoveDisplayMarkerSession(session RemoteExecutor) error {
	// Clean from both bashrc and zshrc since FixDisplaySession writes to both.
	for _, rcFile := range []string{"~/.bashrc", "~/.zshrc"} {
		sedCmd := fmt.Sprintf(
			`sed -i.cc-clip-bak '/%s/,/%s/d' %s 2>/dev/null; rm -f %s.cc-clip-bak`,
			sedEscape(displayMarkerStart),
			sedEscape(displayMarkerEnd),
			rcFile, rcFile)
		// Ignore errors — file might not exist.
		session.Exec(sedCmd)
	}
	return nil
}

// prependBlock prepends a text block to a remote file using a heredoc.
// The heredoc uses a quoted delimiter (<<'CC_CLIP_EOF') to prevent shell
// variable expansion, preserving $HOME, ${DISPLAY:-}, etc. verbatim.
func prependBlock(session RemoteExecutor, rcFile, block string) error {
	// The heredoc delimiter is quoted to prevent variable expansion.
	// The script: create rc if missing, write block + existing content to tmp, mv into place.
	prependCmd := fmt.Sprintf(
		"touch %s && { cat <<'CC_CLIP_EOF'\n%s\nCC_CLIP_EOF\ncat %s; } > %s.cc-clip-tmp && mv %s.cc-clip-tmp %s",
		rcFile, block, rcFile, rcFile, rcFile, rcFile)
	_, err := session.Exec(prependCmd)
	return err
}

// activeRCFiles returns the list of shell rc files that exist on the remote.
// Always includes ~/.bashrc (created if needed). Includes ~/.zshrc only if it
// already exists or zsh is installed.
func activeRCFiles(session RemoteExecutor) []string {
	files := []string{"~/.bashrc"}
	// Add ~/.zshrc if zsh is present (user might use zsh even if $SHELL says bash).
	if _, err := session.Exec("test -f ~/.zshrc || which zsh >/dev/null 2>&1"); err == nil {
		files = append(files, "~/.zshrc")
	}
	return files
}

// sedEscape escapes special characters for use in a sed regex pattern.
func sedEscape(s string) string {
	// Escape forward slashes, brackets, dots, and other regex metacharacters
	replacer := strings.NewReplacer(
		"/", `\/`,
		".", `\.`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"*", `\*`,
		"+", `\+`,
		"?", `\?`,
		"{", `\{`,
		"}", `\}`,
		"^", `\^`,
		"$", `\$`,
	)
	return replacer.Replace(s)
}
