package shim

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// DetectRemoteArch returns the remote system's GOARCH-compatible architecture string.
func DetectRemoteArch(host string) (string, string, error) {
	cmd := exec.Command("ssh", host, "uname -sm")
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
	cmd := exec.Command("scp", localBin, fmt.Sprintf("%s:%s", host, remoteBin))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", string(out), err)
	}

	// Make executable
	cmd = exec.Command("ssh", host, fmt.Sprintf("chmod +x %s", remoteBin))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chmod failed: %s: %w", string(out), err)
	}

	return nil
}

// RemoteExec runs a command on the remote host and returns output.
func RemoteExec(host string, args ...string) (string, error) {
	cmdStr := strings.Join(args, " ")
	cmd := exec.Command("ssh", host, cmdStr)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// WriteRemoteToken writes the session token to the remote host via stdin
// to avoid exposing it in process arguments or shell history.
func WriteRemoteToken(host, token string) error {
	cmd := exec.Command("ssh", host,
		"mkdir -p ~/.cache/cc-clip && cat > ~/.cache/cc-clip/session.token && chmod 600 ~/.cache/cc-clip/session.token")
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
