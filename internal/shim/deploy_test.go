package shim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/install"
)

func TestLocalBinaryHash(t *testing.T) {
	// Create a temp file with known content
	dir := t.TempDir()
	binPath := filepath.Join(dir, "test-binary")
	content := []byte("hello world test binary content")
	if err := os.WriteFile(binPath, content, 0755); err != nil {
		t.Fatalf("failed to write test binary: %v", err)
	}

	hash, err := LocalBinaryHash(binPath)
	if err != nil {
		t.Fatalf("LocalBinaryHash failed: %v", err)
	}

	// Verify format: sha256:<hex>
	if !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("hash should start with 'sha256:', got %q", hash)
	}

	hexPart := strings.TrimPrefix(hash, "sha256:")
	if len(hexPart) != 64 {
		t.Fatalf("expected 64-char hex hash, got %d chars: %q", len(hexPart), hexPart)
	}

	// Same file should produce same hash (deterministic)
	hash2, err := LocalBinaryHash(binPath)
	if err != nil {
		t.Fatalf("second hash failed: %v", err)
	}
	if hash != hash2 {
		t.Fatalf("same file produced different hashes: %q vs %q", hash, hash2)
	}
}

func TestLocalBinaryHashDifferentContent(t *testing.T) {
	dir := t.TempDir()

	bin1 := filepath.Join(dir, "bin1")
	bin2 := filepath.Join(dir, "bin2")

	os.WriteFile(bin1, []byte("content version 1"), 0755)
	os.WriteFile(bin2, []byte("content version 2"), 0755)

	hash1, err := LocalBinaryHash(bin1)
	if err != nil {
		t.Fatal(err)
	}
	hash2, err := LocalBinaryHash(bin2)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Fatal("different content should produce different hashes")
	}
}

func TestLocalBinaryHashNonExistent(t *testing.T) {
	_, err := LocalBinaryHash("/nonexistent/path/binary")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestNeedsUpload(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "test-binary")
	os.WriteFile(binPath, []byte("binary content"), 0755)

	hash, _ := LocalBinaryHash(binPath)

	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name:   "nil remote state",
			remote: nil,
			want:   true,
		},
		{
			name: "matching hash",
			remote: &DeployState{
				BinaryHash: hash,
			},
			want: false,
		},
		{
			name: "different hash",
			remote: &DeployState{
				BinaryHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			},
			want: true,
		},
		{
			name: "empty hash in remote state",
			remote: &DeployState{
				BinaryHash: "",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsUpload(binPath, tt.remote)
			if got != tt.want {
				t.Errorf("NeedsUpload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsUploadNonExistentBinary(t *testing.T) {
	// If local binary doesn't exist, NeedsUpload returns true
	remote := &DeployState{BinaryHash: "sha256:abc"}
	got := NeedsUpload("/nonexistent/binary", remote)
	if !got {
		t.Error("expected NeedsUpload=true when local binary doesn't exist")
	}
}

func TestReadRemoteStateMissingFile(t *testing.T) {
	s := &localSession{home: t.TempDir()}

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState returned error for missing state file: %v", err)
	}
	if state != nil {
		t.Fatalf("ReadRemoteState returned state for missing file: %+v", state)
	}
}

func TestReadRemoteStateValidJSON(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:deadbeef",
  "binary_version": "v0.7.2",
  "shim_installed": true,
  "shim_target": "xclip",
  "path_fixed": true
}`)

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState returned error: %v", err)
	}
	if state == nil {
		t.Fatal("ReadRemoteState returned nil state")
	}
	if state.BinaryHash != "sha256:deadbeef" {
		t.Fatalf("BinaryHash = %q, want %q", state.BinaryHash, "sha256:deadbeef")
	}
	if state.BinaryVersion != "v0.7.2" {
		t.Fatalf("BinaryVersion = %q, want %q", state.BinaryVersion, "v0.7.2")
	}
	if !state.ShimInstalled || state.ShimTarget != "xclip" || !state.PathFixed {
		t.Fatalf("unexpected state: %+v", state)
	}
}

func TestReadRemoteStateMalformedJSONReturnsError(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{broken json`)

	state, err := ReadRemoteState(s)
	if err == nil {
		t.Fatal("ReadRemoteState returned nil error for malformed deploy state")
	}
	if state != nil {
		t.Fatalf("ReadRemoteState returned state for malformed JSON: %+v", state)
	}
}

func TestReadRemoteStateEmptyFileReturnsError(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, "")

	state, err := ReadRemoteState(s)
	if err == nil {
		t.Fatal("ReadRemoteState returned nil error for empty deploy state")
	}
	if state != nil {
		t.Fatalf("ReadRemoteState returned state for empty JSON: %+v", state)
	}
}

func TestNeedsShimInstall(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name:   "nil remote state",
			remote: nil,
			want:   true,
		},
		{
			name: "shim not installed",
			remote: &DeployState{
				ShimInstalled: false,
			},
			want: true,
		},
		{
			name: "shim installed",
			remote: &DeployState{
				ShimInstalled: true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsShimInstall(tt.remote)
			if got != tt.want {
				t.Errorf("NeedsShimInstall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeployStateJSON(t *testing.T) {
	state := DeployState{
		BinaryHash:    "sha256:abc123",
		BinaryVersion: "v0.1.0",
		ShimInstalled: true,
		ShimTarget:    "xclip",
		PathFixed:     true,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded DeployState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.BinaryHash != state.BinaryHash {
		t.Errorf("BinaryHash mismatch: %q vs %q", decoded.BinaryHash, state.BinaryHash)
	}
	if decoded.BinaryVersion != state.BinaryVersion {
		t.Errorf("BinaryVersion mismatch: %q vs %q", decoded.BinaryVersion, state.BinaryVersion)
	}
	if decoded.ShimInstalled != state.ShimInstalled {
		t.Errorf("ShimInstalled mismatch: %v vs %v", decoded.ShimInstalled, state.ShimInstalled)
	}
	if decoded.ShimTarget != state.ShimTarget {
		t.Errorf("ShimTarget mismatch: %q vs %q", decoded.ShimTarget, state.ShimTarget)
	}
	if decoded.PathFixed != state.PathFixed {
		t.Errorf("PathFixed mismatch: %v vs %v", decoded.PathFixed, state.PathFixed)
	}
}

// TestDeployStateOpencodeNotifyRoundTrip verifies that after marking the
// opencode-notify adapter installed, AdapterInstalled reports it true and the
// entry (Installed=true, Verified=false) survives a marshal/unmarshal round-trip.
// Verified stays false because a successful plugin drop proves the file landed,
// not that opencode loads it or that session.idle fires.
func TestDeployStateOpencodeNotifyRoundTrip(t *testing.T) {
	state := &DeployState{
		Notify: &NotifyDeployState{
			Enabled: true,
			Adapters: map[AdapterID]*AdapterState{
				AdapterOpencodeNotify: {Installed: true, Source: install.SourceConfig, Verified: false},
			},
		},
	}
	if !AdapterInstalled(state, AdapterOpencodeNotify) {
		t.Fatal("AdapterInstalled(opencode-notify) must be true after install")
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	var decoded DeployState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !AdapterInstalled(&decoded, AdapterOpencodeNotify) {
		t.Fatal("opencode-notify adapter must survive marshal/unmarshal as installed")
	}
	if a := decoded.Notify.Adapters[AdapterOpencodeNotify]; a == nil || a.Verified {
		t.Fatalf("opencode-notify adapter must round-trip with Verified=false: %+v", a)
	}
}

func TestDeployStateJSONFromRaw(t *testing.T) {
	// Simulate reading from a remote file
	raw := `{
  "binary_hash": "sha256:deadbeef",
  "binary_version": "v0.2.0",
  "shim_installed": true,
  "shim_target": "wl-paste",
  "path_fixed": false
}`

	var state DeployState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("failed to unmarshal raw JSON: %v", err)
	}

	if state.BinaryHash != "sha256:deadbeef" {
		t.Errorf("unexpected BinaryHash: %q", state.BinaryHash)
	}
	if state.BinaryVersion != "v0.2.0" {
		t.Errorf("unexpected BinaryVersion: %q", state.BinaryVersion)
	}
	if !state.ShimInstalled {
		t.Error("expected ShimInstalled=true")
	}
	if state.ShimTarget != "wl-paste" {
		t.Errorf("unexpected ShimTarget: %q", state.ShimTarget)
	}
	if state.PathFixed {
		t.Error("expected PathFixed=false")
	}
}

func TestDeployStateCorruptedJSON(t *testing.T) {
	// Corrupted JSON should not parse
	raw := `{broken json`
	var state DeployState
	err := json.Unmarshal([]byte(raw), &state)
	if err == nil {
		t.Error("expected error for corrupted JSON")
	}
}

func TestDeployStateJSONCodexNil(t *testing.T) {
	// Marshal with Codex: nil -> no "codex" key in JSON
	state := DeployState{
		BinaryHash:    "sha256:abc",
		BinaryVersion: "v0.1.0",
		ShimInstalled: true,
		ShimTarget:    "xclip",
		PathFixed:     true,
		Codex:         nil,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	raw := string(data)
	if strings.Contains(raw, `"codex"`) {
		t.Fatalf("JSON should not contain 'codex' key when Codex is nil, got: %s", raw)
	}
}

func writeRemoteDeployState(t *testing.T, home, content string) {
	t.Helper()

	stateDir := filepath.Join(home, ".cache", "cc-clip")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create remote state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "deploy.json"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write remote deploy state: %v", err)
	}
}

func TestDeployStateJSONCodexPopulated(t *testing.T) {
	// Marshal with Codex populated -> JSON has "codex" block
	state := DeployState{
		BinaryHash:    "sha256:abc",
		BinaryVersion: "v0.1.0",
		ShimInstalled: true,
		ShimTarget:    "xclip",
		PathFixed:     true,
		Codex: &CodexDeployState{
			Enabled:      true,
			Mode:         "xvfb",
			DisplayFixed: true,
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"codex"`) {
		t.Fatalf("JSON should contain 'codex' key, got: %s", raw)
	}

	// Round-trip unmarshal
	var decoded DeployState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Codex == nil {
		t.Fatal("decoded Codex should not be nil")
	}
	if !decoded.Codex.Enabled {
		t.Error("decoded Codex.Enabled should be true")
	}
	if decoded.Codex.Mode != "xvfb" {
		t.Errorf("decoded Codex.Mode = %q, want %q", decoded.Codex.Mode, "xvfb")
	}
	if !decoded.Codex.DisplayFixed {
		t.Error("decoded Codex.DisplayFixed should be true")
	}
}

func TestDeployStateJSONUnmarshalOldFormat(t *testing.T) {
	// Unmarshal old JSON (no codex field) -> Codex: nil, no error
	raw := `{
  "binary_hash": "sha256:deadbeef",
  "binary_version": "v0.2.0",
  "shim_installed": true,
  "shim_target": "wl-paste",
  "path_fixed": false
}`

	var state DeployState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("failed to unmarshal old format: %v", err)
	}

	if state.Codex != nil {
		t.Fatalf("Codex should be nil for old format JSON, got: %+v", state.Codex)
	}

	// Verify other fields still work
	if state.BinaryHash != "sha256:deadbeef" {
		t.Errorf("unexpected BinaryHash: %q", state.BinaryHash)
	}
}

func TestDeployStatePersistsNotificationSetup(t *testing.T) {
	state := &DeployState{
		BinaryHash: "sha256:abc",
		Notify: &NotifyDeployState{
			Enabled:        true,
			HookInstalled:  true,
			CodexInjected:  true,
			HealthVerified: true,
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(raw), `"notify"`) {
		t.Fatalf("expected notify block, got %s", raw)
	}

	// Round-trip unmarshal
	var decoded DeployState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Notify == nil {
		t.Fatal("decoded Notify should not be nil")
	}
	if !decoded.Notify.Enabled {
		t.Error("decoded Notify.Enabled should be true")
	}
	if !decoded.Notify.HookInstalled {
		t.Error("decoded Notify.HookInstalled should be true")
	}
	if !decoded.Notify.CodexInjected {
		t.Error("decoded Notify.CodexInjected should be true")
	}
	if !decoded.Notify.HealthVerified {
		t.Error("decoded Notify.HealthVerified should be true")
	}
}

func TestDeployStateJSONNotifyNil(t *testing.T) {
	// Marshal with Notify: nil -> no "notify" key in JSON
	state := DeployState{
		BinaryHash:    "sha256:abc",
		BinaryVersion: "v0.1.0",
		ShimInstalled: true,
		Notify:        nil,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	raw := string(data)
	if strings.Contains(raw, `"notify"`) {
		t.Fatalf("JSON should not contain 'notify' key when Notify is nil, got: %s", raw)
	}
}

func TestDeployStateJSONUnmarshalOldFormatNoNotify(t *testing.T) {
	// Unmarshal old JSON (no notify field) -> Notify: nil, no error
	raw := `{
  "binary_hash": "sha256:deadbeef",
  "binary_version": "v0.2.0",
  "shim_installed": true,
  "shim_target": "wl-paste",
  "path_fixed": false
}`

	var state DeployState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("failed to unmarshal old format: %v", err)
	}

	if state.Notify != nil {
		t.Fatalf("Notify should be nil for old format JSON, got: %+v", state.Notify)
	}
}

func TestNeedsNotifySetup(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name:   "nil remote state",
			remote: nil,
			want:   true,
		},
		{
			name:   "nil Notify field",
			remote: &DeployState{},
			want:   true,
		},
		{
			name: "Notify not enabled",
			remote: &DeployState{
				Notify: &NotifyDeployState{
					Enabled: false,
				},
			},
			want: true,
		},
		{
			name: "Notify enabled",
			remote: &DeployState{
				Notify: &NotifyDeployState{
					Enabled:        true,
					HookInstalled:  true,
					HealthVerified: true,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsNotifySetup(tt.remote)
			if got != tt.want {
				t.Errorf("NeedsNotifySetup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsCodexSetup(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name:   "nil remote state",
			remote: nil,
			want:   true,
		},
		{
			name:   "nil Codex field",
			remote: &DeployState{},
			want:   true,
		},
		{
			name: "Codex not enabled",
			remote: &DeployState{
				Codex: &CodexDeployState{
					Enabled: false,
				},
			},
			want: true,
		},
		{
			name: "Codex enabled",
			remote: &DeployState{
				Codex: &CodexDeployState{
					Enabled: true,
					Mode:    "xvfb",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsCodexSetup(tt.remote)
			if got != tt.want {
				t.Errorf("NeedsCodexSetup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeployState_ClaudeWrapperRoundtrip(t *testing.T) {
	in := &DeployState{
		BinaryHash:    "sha256:abc",
		BinaryVersion: "v0.7.1-test",
		ShimInstalled: true,
		ShimTarget:    "xclip",
		ClaudeWrapper: &ClaudeWrapperState{
			Installed:    true,
			OriginKind:   "symlink",
			OriginTarget: "/home/u/.local/share/claude/versions/2.1.132",
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out DeployState
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ClaudeWrapper == nil {
		t.Fatal("ClaudeWrapper missing after roundtrip")
	}
	if out.ClaudeWrapper.OriginKind != "symlink" {
		t.Fatalf("OriginKind: got %q, want symlink", out.ClaudeWrapper.OriginKind)
	}
}

func TestDeployState_ClaudeWrapperOmitemptyWhenNil(t *testing.T) {
	in := &DeployState{BinaryHash: "x"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "claude_wrapper") {
		t.Fatalf("nil ClaudeWrapper should be omitted, got: %s", data)
	}
}

// --- Step 1: per-adapter schema migration tests ---

func TestMigrateLegacyNotifyState_AllBooleansTrue(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:legacy",
  "binary_version": "v0.8.0",
  "shim_installed": true,
  "shim_target": "xclip",
  "path_fixed": true,
  "notify": {"enabled": true, "hook_installed": true, "codex_injected": true, "health_verified": true}
}`)

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState error: %v", err)
	}
	if state == nil || state.Notify == nil {
		t.Fatalf("expected Notify state, got %+v", state)
	}
	if state.Notify.Adapters == nil {
		t.Fatal("expected Adapters map after migration")
	}

	claude := state.Notify.Adapters[AdapterClaudeNotify]
	if claude == nil {
		t.Fatal("expected claude-notify adapter")
	}
	if !claude.Installed {
		t.Error("claude-notify Installed should be true")
	}
	if claude.Verified {
		t.Error("claude-notify Verified should be migrated as false")
	}
	if claude.Source != install.SourceConfig {
		t.Errorf("claude-notify Source = %q, want %q", claude.Source, install.SourceConfig)
	}

	codex := state.Notify.Adapters[AdapterCodexNotify]
	if codex == nil {
		t.Fatal("expected codex-notify adapter")
	}
	if !codex.Installed {
		t.Error("codex-notify Installed should be true")
	}
	if codex.Verified {
		t.Error("codex-notify Verified should be migrated as false")
	}
	if codex.Source != install.SourceConfig {
		t.Errorf("codex-notify Source = %q, want %q", codex.Source, install.SourceConfig)
	}

	if _, ok := state.Notify.Adapters[AdapterOpencodeNotify]; ok {
		t.Error("opencode-notify adapter should be absent")
	}
}

func TestMigrateLegacyNotifyState_PartialBooleans(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:legacy",
  "notify": {"enabled": true, "hook_installed": true, "codex_injected": false, "health_verified": false}
}`)

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState error: %v", err)
	}
	if state == nil || state.Notify == nil || state.Notify.Adapters == nil {
		t.Fatalf("expected migrated Notify state, got %+v", state)
	}

	claude := state.Notify.Adapters[AdapterClaudeNotify]
	if claude == nil {
		t.Fatal("expected claude-notify adapter")
	}
	if !claude.Installed {
		t.Error("claude-notify Installed should be true")
	}
	if claude.Verified {
		t.Error("claude-notify Verified should be false")
	}

	if _, ok := state.Notify.Adapters[AdapterCodexNotify]; ok {
		t.Error("codex-notify adapter should be absent when codex_injected=false")
	}
}

func TestReadRemoteState_NewSchemaUnchanged(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:new",
  "notify": {
    "enabled": true,
    "adapters": {
      "claude-notify": {"installed": true, "source": "marketplace", "version": "1.2.3", "verified": true}
    }
  }
}`)

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState error: %v", err)
	}
	if state == nil || state.Notify == nil || state.Notify.Adapters == nil {
		t.Fatalf("expected Notify adapters, got %+v", state)
	}

	claude := state.Notify.Adapters[AdapterClaudeNotify]
	if claude == nil {
		t.Fatal("expected claude-notify adapter preserved")
	}
	if !claude.Installed || !claude.Verified {
		t.Errorf("claude-notify should be preserved Installed+Verified, got %+v", claude)
	}
	if claude.Source != install.SourceMarketplace {
		t.Errorf("claude-notify Source = %q, want %q", claude.Source, install.SourceMarketplace)
	}
	if claude.Version != "1.2.3" {
		t.Errorf("claude-notify Version = %q, want 1.2.3", claude.Version)
	}

	// migrate must be a no-op: no synthesized codex-notify entry
	if _, ok := state.Notify.Adapters[AdapterCodexNotify]; ok {
		t.Error("codex-notify should not be synthesized when adapters map already present")
	}
}

func TestReadRemoteState_LegacyUnmarshalsWithoutError(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:deadbeef",
  "binary_version": "v0.8.1",
  "shim_installed": true,
  "shim_target": "wl-paste",
  "path_fixed": false,
  "notify": {"enabled": true, "hook_installed": true, "codex_injected": true, "health_verified": true}
}`)

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("legacy deploy.json should unmarshal without error, got: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
}

func TestMigrateNotifyState_NilSafe(t *testing.T) {
	// Direct nil pointer
	migrateNotifyState(nil)

	// DeployState with Notify == nil through ReadRemoteState
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:abc",
  "shim_installed": true
}`)
	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState error: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.Notify != nil {
		t.Fatalf("Notify should remain nil, got %+v", state.Notify)
	}
}

func TestMigrateNotifyState_Idempotent(t *testing.T) {
	n := &NotifyDeployState{
		Enabled:       true,
		HookInstalled: true,
		CodexInjected: true,
	}
	migrateNotifyState(n)

	// Snapshot after first migration
	first, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	migrateNotifyState(n)

	second, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("second migration mutated state:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestNeedsAdapterInstall(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name:   "nil DeployState",
			remote: nil,
			want:   true,
		},
		{
			name: "installed false",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{
					AdapterClaudeNotify: {Installed: false},
				},
			}},
			want: true,
		},
		{
			name: "installed true",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{
					AdapterClaudeNotify: {Installed: true},
				},
			}},
			want: false,
		},
		{
			name: "absent entry",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsAdapterInstall(tt.remote, AdapterClaudeNotify)
			if got != tt.want {
				t.Errorf("NeedsAdapterInstall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsAdapterVerify(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name: "installed true, verified false",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{
					AdapterClaudeNotify: {Installed: true, Verified: false},
				},
			}},
			want: true,
		},
		{
			name: "installed true, verified true",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{
					AdapterClaudeNotify: {Installed: true, Verified: true},
				},
			}},
			want: false,
		},
		{
			name: "absent entry",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{},
			}},
			want: false,
		},
		{
			name:   "nil DeployState",
			remote: nil,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsAdapterVerify(tt.remote, AdapterClaudeNotify)
			if got != tt.want {
				t.Errorf("NeedsAdapterVerify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdapterInstalled(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{
			name: "present and installed",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{
					AdapterClaudeNotify: {Installed: true},
				},
			}},
			want: true,
		},
		{
			name: "present but not installed",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{
					AdapterClaudeNotify: {Installed: false},
				},
			}},
			want: false,
		},
		{
			name: "absent entry",
			remote: &DeployState{Notify: &NotifyDeployState{
				Adapters: map[AdapterID]*AdapterState{},
			}},
			want: false,
		},
		{
			name:   "nil DeployState",
			remote: nil,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdapterInstalled(tt.remote, AdapterClaudeNotify)
			if got != tt.want {
				t.Errorf("AdapterInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsNotifySetup_BackwardCompat(t *testing.T) {
	tests := []struct {
		name   string
		remote *DeployState
		want   bool
	}{
		{name: "nil remote", remote: nil, want: true},
		{name: "Notify nil", remote: &DeployState{}, want: true},
		{name: "Enabled false", remote: &DeployState{Notify: &NotifyDeployState{Enabled: false}}, want: true},
		{name: "Enabled true", remote: &DeployState{Notify: &NotifyDeployState{Enabled: true}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsNotifySetup(tt.remote); got != tt.want {
				t.Errorf("NeedsNotifySetup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClipboardTransportPreserved(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeRemoteDeployState(t, s.home, `{
  "binary_hash": "sha256:transport",
  "binary_version": "v0.8.0",
  "shim_installed": true,
  "shim_target": "xclip",
  "path_fixed": true,
  "codex": {"enabled": true, "mode": "xvfb", "display_fixed": true},
  "notify": {"enabled": true, "hook_installed": true, "codex_injected": true, "health_verified": true}
}`)

	state, err := ReadRemoteState(s)
	if err != nil {
		t.Fatalf("ReadRemoteState error: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}

	// Transport fields must be byte-equivalent / unchanged by migration.
	if state.BinaryHash != "sha256:transport" {
		t.Errorf("BinaryHash = %q, want sha256:transport", state.BinaryHash)
	}
	if state.BinaryVersion != "v0.8.0" {
		t.Errorf("BinaryVersion = %q, want v0.8.0", state.BinaryVersion)
	}
	if !state.ShimInstalled {
		t.Error("ShimInstalled should be true")
	}
	if state.ShimTarget != "xclip" {
		t.Errorf("ShimTarget = %q, want xclip", state.ShimTarget)
	}
	if !state.PathFixed {
		t.Error("PathFixed should be true")
	}
	if state.Codex == nil {
		t.Fatal("Codex transport state should be preserved")
	}
	if !state.Codex.Enabled || state.Codex.Mode != "xvfb" || !state.Codex.DisplayFixed {
		t.Errorf("Codex transport state mutated: %+v", state.Codex)
	}
}
