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

	"github.com/shunmei/cc-clip/internal/install"
)

// CodexDeployState represents the Codex-specific deployment state.
type CodexDeployState struct {
	Enabled      bool   `json:"enabled"`
	Mode         string `json:"mode"`
	DisplayFixed bool   `json:"display_fixed"`
}

// AdapterID identifies a notification adapter within the deploy state.
type AdapterID string

const (
	// AdapterClaudeNotify is the Claude Code notification adapter.
	AdapterClaudeNotify AdapterID = "claude-notify"
	// AdapterCodexNotify is the Codex CLI notification adapter.
	AdapterCodexNotify AdapterID = "codex-notify"
	// AdapterOpencodeNotify is the opencode notification adapter.
	AdapterOpencodeNotify AdapterID = "opencode-notify"
	// AdapterAntigravityNotify is the Antigravity (agy CLI) notification adapter.
	// The string MUST match plugin.AdapterAntigravityNotify (the runner dispatcher
	// key) so connect records deploy-state under the key the runner actually uses.
	AdapterAntigravityNotify AdapterID = "agy-notify"
)

// AdapterState records the per-adapter installation/verification status.
type AdapterState struct {
	Installed bool                  `json:"installed"`
	Source    install.AdapterSource `json:"source,omitempty"`
	Version   string                `json:"version,omitempty"`
	Verified  bool                  `json:"verified"`
	LastError string                `json:"last_error,omitempty"`
}

// NotifyDeployState represents the notification bridge deployment state.
//
// The legacy boolean fields (Enabled/HookInstalled/CodexInjected/HealthVerified)
// are retained through v0.x for backward read-compat. Per-adapter state lives in
// Adapters, populated either directly by newer writers or via migrateNotifyState
// for state files written by older versions.
type NotifyDeployState struct {
	Enabled        bool `json:"enabled"`
	HookInstalled  bool `json:"hook_installed"`
	CodexInjected  bool `json:"codex_injected"`
	HealthVerified bool `json:"health_verified"`

	Adapters map[AdapterID]*AdapterState `json:"adapters,omitempty"`
}

// ClaudeWrapperState records what InstallRemoteClaudeWrapper replaced at
// ~/.local/bin/claude. Used by uninstall and future doctor commands; the
// wrapper bash script itself does NOT read this — it only checks file
// existence/executability of the sidecar.
type ClaudeWrapperState struct {
	Installed    bool   `json:"installed"`
	OriginKind   string `json:"origin_kind"`             // "none" | "regular" | "symlink"
	OriginTarget string `json:"origin_target,omitempty"` // resolved path when OriginKind=="symlink"
}

// currentDeploySchemaVersion is the deploy-state schema version this binary
// writes. v1 is the v0.9.0 per-adapter Notify schema. A state stamped with a
// HIGHER version was written by a newer cc-clip; the connect path refuses to
// overwrite it (see DeployState.IsNewerSchema). A state with SchemaVersion==0
// (legacy / pre-guard) is NOT newer — it migrates normally via migrateNotifyState.
const currentDeploySchemaVersion = 1

// DeployState represents the state of a cc-clip deployment on a remote host.
// It is stored as ~/.cache/cc-clip/deploy.json on the remote.
type DeployState struct {
	SchemaVersion int                 `json:"schema_version,omitempty"`
	BinaryHash    string              `json:"binary_hash"`
	BinaryVersion string              `json:"binary_version"`
	ShimInstalled bool                `json:"shim_installed"`
	ShimTarget    string              `json:"shim_target"`
	PathFixed     bool                `json:"path_fixed"`
	Notify        *NotifyDeployState  `json:"notify,omitempty"`
	Codex         *CodexDeployState   `json:"codex,omitempty"`
	ClaudeWrapper *ClaudeWrapperState `json:"claude_wrapper,omitempty"`
}

// CurrentDeploySchemaVersion returns the deploy-state schema version this
// binary writes. Exposed so the connect path can render an actionable
// "this binary's vN" message in the forward-downgrade guard.
func CurrentDeploySchemaVersion() int { return currentDeploySchemaVersion }

// IsNewerSchema reports whether this state was written by a cc-clip with a
// HIGHER deploy-state schema version than the running binary. Only
// SchemaVersion strictly greater than currentDeploySchemaVersion counts as
// newer; SchemaVersion==0 (legacy / pre-guard) is treated as older and
// migrates normally. The connect path uses this to refuse clobbering a remote
// deployed by a newer cc-clip unless --force is given. A nil receiver is safe
// and reports false.
func (s *DeployState) IsNewerSchema() bool {
	return s != nil && s.SchemaVersion > currentDeploySchemaVersion
}

// stampSchemaVersion sets state.SchemaVersion to the version this binary
// writes. Applied by WriteRemoteState before marshaling so every state this
// binary persists carries its schema version forward.
func stampSchemaVersion(state *DeployState) {
	if state == nil {
		return
	}
	// Never LOWER an existing schema version. An older binary re-writing a
	// state deployed by a NEWER cc-clip (e.g. `uninstall --codex`, or
	// `connect --force`) must not silently downgrade schema_version — that
	// would mask the very newer-schema condition IsNewerSchema/the connect
	// guard exist to surface. Stamp forward only when the existing value is
	// older than this binary's.
	if state.SchemaVersion < currentDeploySchemaVersion {
		state.SchemaVersion = currentDeploySchemaVersion
	}
}

const remoteDeployPath = "~/.cache/cc-clip/deploy.json"
const remoteStateNotFound = "__CC_CLIP_DEPLOY_STATE_NOT_FOUND__"

// ReadRemoteState reads the deploy state from the remote host via the SSH session.
// Returns nil (not an error) when the state file does not exist.
func ReadRemoteState(session RemoteExecutor) (*DeployState, error) {
	readCmd := fmt.Sprintf("if [ ! -e %s ]; then printf '%s\\n'; else cat %s; fi", remoteDeployPath, remoteStateNotFound, remoteDeployPath)
	out, err := session.Exec(readCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote deploy state: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == remoteStateNotFound {
		return nil, nil
	}
	if out == "" {
		return nil, fmt.Errorf("remote deploy state is empty")
	}

	var state DeployState
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		return nil, fmt.Errorf("remote deploy state is malformed: %w", err)
	}

	migrateNotifyState(state.Notify)

	return &state, nil
}

// migrateNotifyState upgrades a legacy boolean-only NotifyDeployState to the
// per-adapter schema. It is a no-op when n is nil or already carries an Adapters
// map (i.e. written by a newer version). Legacy boolean fields are deliberately
// left intact for backward read-compat. Verified is migrated as false to force
// re-verification regardless of the legacy HealthVerified value.
func migrateNotifyState(n *NotifyDeployState) {
	if n == nil || n.Adapters != nil {
		return
	}

	n.Adapters = make(map[AdapterID]*AdapterState)
	if n.HookInstalled {
		n.Adapters[AdapterClaudeNotify] = &AdapterState{
			Installed: true,
			Source:    install.SourceConfig,
			Verified:  false,
		}
	}
	if n.CodexInjected {
		n.Adapters[AdapterCodexNotify] = &AdapterState{
			Installed: true,
			Source:    install.SourceConfig,
			Verified:  false,
		}
	}
}

// adapterState returns the AdapterState for id, or nil if any level (remote,
// Notify, Adapters, or the entry) is absent.
func adapterState(remote *DeployState, id AdapterID) *AdapterState {
	if remote == nil || remote.Notify == nil || remote.Notify.Adapters == nil {
		return nil
	}
	return remote.Notify.Adapters[id]
}

// NeedsAdapterInstall reports whether adapter id must be (re-)installed: true
// when the entry is absent or its Installed flag is false.
func NeedsAdapterInstall(remote *DeployState, id AdapterID) bool {
	st := adapterState(remote, id)
	if st == nil {
		return true
	}
	return !st.Installed
}

// NeedsAdapterVerify reports whether adapter id is installed but not yet verified.
func NeedsAdapterVerify(remote *DeployState, id AdapterID) bool {
	st := adapterState(remote, id)
	if st == nil {
		return false
	}
	return st.Installed && !st.Verified
}

// AdapterInstalled reports whether adapter id is present and marked installed.
func AdapterInstalled(remote *DeployState, id AdapterID) bool {
	st := adapterState(remote, id)
	if st == nil {
		return false
	}
	return st.Installed
}

// WriteRemoteState writes the deploy state to the remote host via the SSH session.
func WriteRemoteState(session *SSHSession, state *DeployState) error {
	stampSchemaVersion(state)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal deploy state: %w", err)
	}

	// Write via stdin to avoid shell escaping issues with JSON
	remoteCmd := fmt.Sprintf("mkdir -p ~/.cache/cc-clip && cat > %s", remoteDeployPath)
	c := exec.Command("ssh", session.sshArgs(remoteCmd)...)
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

// NeedsNotifySetup checks whether notification bridge setup is needed on the remote.
func NeedsNotifySetup(remote *DeployState) bool {
	if remote == nil || remote.Notify == nil {
		return true
	}
	return !remote.Notify.Enabled
}

// NeedsCodexSetup checks whether Codex setup is needed on the remote.
func NeedsCodexSetup(remote *DeployState) bool {
	if remote == nil || remote.Codex == nil {
		return true
	}
	return !remote.Codex.Enabled
}
