package shim

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// noForwardPrefix guards the one-shot package-level ssh/scp host helpers
// (DetectRemoteArch, UploadBinary, RemoteExec, WriteRemoteToken) so they do
// not inherit the operator's global RemoteForward. Each invocation is
// independent (no ControlMaster), so without ClearAllForwardings=yes a
// pre-existing RemoteForward would compete with the active cc-clip tunnel
// (see CLAUDE.md "Known Pitfalls": ControlMaster + RemoteForward). The
// SSHSession path guards this via connArgs(); these legacy helpers mirror it.
var noForwardPrefix = []string{"-o", "ClearAllForwardings=yes"}

// detectRemoteArchArgs builds the ssh arg vector for DetectRemoteArch.
func detectRemoteArchArgs(host string) []string {
	return sshHostArgs(noForwardPrefix, host, "uname -sm")
}

// uploadBinaryScpArgs builds the scp arg vector for UploadBinary.
func uploadBinaryScpArgs(host, localBin, remoteBin string) []string {
	return scpUploadArgs(noForwardPrefix, localBin, host, remoteBin)
}

// uploadBinaryChmodArgs builds the ssh chmod arg vector for UploadBinary.
func uploadBinaryChmodArgs(host, remoteBin string) []string {
	return sshHostArgs(noForwardPrefix, host, fmt.Sprintf("chmod +x %s", remoteBin))
}

// remoteExecArgs builds the ssh arg vector for RemoteExec.
func remoteExecArgs(host, cmdStr string) []string {
	return sshHostArgs(noForwardPrefix, host, cmdStr)
}

// writeRemoteTokenArgs builds the ssh arg vector for WriteRemoteToken.
func writeRemoteTokenArgs(host string) []string {
	return sshHostArgs(noForwardPrefix, host,
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.token && chmod 600 ~/.cache/cc-clip/session.token")
}

// DetectRemoteArch returns the remote system's GOARCH-compatible architecture string.
func DetectRemoteArch(host string) (string, string, error) {
	cmd := exec.Command("ssh", detectRemoteArchArgs(host)...)
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to detect remote arch: %w", err)
	}

	parts := strings.Fields(strings.TrimSpace(string(out)))
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

// LocalBinaryPath returns the path of the currently running cc-clip binary.
func LocalBinaryPath() (string, error) {
	path, err := exec.LookPath("cc-clip")
	if err != nil {
		// Fallback: try to find in common locations
		candidates := []string{
			"/usr/local/bin/cc-clip",
			fmt.Sprintf("%s/.local/bin/cc-clip", homeDir()),
		}
		for _, c := range candidates {
			if _, err := exec.LookPath(c); err == nil {
				return c, nil
			}
		}
		return "", fmt.Errorf("cc-clip binary not found in PATH")
	}
	return path, nil
}

// UploadBinary copies the cc-clip binary to the remote host.
func UploadBinary(host, localBin, remoteBin string) error {
	cmd := exec.Command("scp", uploadBinaryScpArgs(host, localBin, remoteBin)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", string(out), err)
	}

	// Make executable
	cmd = exec.Command("ssh", uploadBinaryChmodArgs(host, remoteBin)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chmod failed: %s: %w", string(out), err)
	}

	return nil
}

// RemoteExec runs a command on the remote host and returns output.
func RemoteExec(host string, args ...string) (string, error) {
	cmdStr := strings.Join(args, " ")
	cmd := exec.Command("ssh", remoteExecArgs(host, cmdStr)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// WriteRemoteToken writes the session token to the remote host via stdin
// to avoid exposing it in process arguments or shell history.
func WriteRemoteToken(host, token string) error {
	cmd := exec.Command("ssh", writeRemoteTokenArgs(host)...)
	cmd.Stdin = strings.NewReader(token + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write remote token: %s: %w", string(out), err)
	}
	return nil
}

// NeedsCrossBuild returns true if the remote arch differs from local.
func NeedsCrossBuild(remoteOS, remoteArch string) bool {
	return remoteOS != runtime.GOOS || remoteArch != runtime.GOARCH
}

func homeDir() string {
	home, _ := exec.Command("sh", "-c", "echo $HOME").Output()
	return strings.TrimSpace(string(home))
}
