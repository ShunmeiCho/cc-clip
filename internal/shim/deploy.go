package shim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CodexDeployState represents the Codex-specific deployment state.
type CodexDeployState struct {
	Enabled      bool   `json:"enabled"`
	Mode         string `json:"mode"`
	DisplayFixed bool   `json:"display_fixed"`
}

// DeployState represents the state of a cc-clip deployment on a remote host.
// It is stored as ~/.cache/cc-clip/deploy.json on the remote.
type DeployState struct {
	BinaryHash    string           `json:"binary_hash"`
	BinaryVersion string           `json:"binary_version"`
	ShimInstalled bool             `json:"shim_installed"`
	ShimTarget    string           `json:"shim_target"`
	PathFixed     bool             `json:"path_fixed"`
	Codex         *CodexDeployState `json:"codex,omitempty"`
}

const remoteDeployPath = "~/.cache/cc-clip/deploy.json"

// ReadRemoteState reads the deploy state from the remote host via the SSH session.
// Returns nil (not an error) when the state file does not exist.
func ReadRemoteState(session *SSHSession) (*DeployState, error) {
	out, err := session.Exec(fmt.Sprintf("cat %s 2>/dev/null || echo '__NOTFOUND__'", remoteDeployPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read remote deploy state: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == "__NOTFOUND__" || out == "" {
		return nil, nil
	}

	var state DeployState
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		// Corrupted state file — treat as missing
		return nil, nil
	}

	return &state, nil
}

// WriteRemoteState writes the deploy state to the remote host via the SSH session.
func WriteRemoteState(session *SSHSession, state *DeployState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal deploy state: %w", err)
	}

	// Write via stdin to avoid shell escaping issues with JSON
	remoteCmd := fmt.Sprintf("mkdir -p ~/.cache/cc-clip && cat > %s", remoteDeployPath)
	args := []string{}
	if session.controlPath != "" {
		args = append(args, "-o", fmt.Sprintf("ControlPath=%s", session.controlPath))
	}
	args = append(args, session.host, remoteCmd)
	c := exec.Command("ssh", args...)
	c.Stdin = strings.NewReader(string(data) + "\n")
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write remote deploy state: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// LocalBinaryHash computes the SHA-256 hash of a local file.
// Returns the hash as "sha256:<hex>" string.
func LocalBinaryHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open binary for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash binary: %w", err)
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// NeedsUpload compares the local binary hash with the remote deploy state
// to determine if an upload is necessary.
func NeedsUpload(localBinPath string, remote *DeployState) bool {
	if remote == nil {
		return true
	}

	localHash, err := LocalBinaryHash(localBinPath)
	if err != nil {
		// Cannot hash local binary — assume upload is needed
		return true
	}

	return localHash != remote.BinaryHash
}

// NeedsShimInstall checks whether the shim needs to be (re-)installed.
func NeedsShimInstall(remote *DeployState) bool {
	if remote == nil {
		return true
	}
	return !remote.ShimInstalled
}

// NeedsCodexSetup checks whether Codex setup is needed on the remote.
func NeedsCodexSetup(remote *DeployState) bool {
	if remote == nil || remote.Codex == nil {
		return true
	}
	return !remote.Codex.Enabled
}
