package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shunmei/cc-clip/internal/install"
	"github.com/shunmei/cc-clip/internal/shim"
)

// TestTargetMembership verifies the per-target membership predicates that gate
// 5.3c's per-phase side effects: Claude is targeted iff t.Claude; Codex iff
// t.Codex; the clipboard shim is targeted iff Claude OR Opencode (Codex uses
// X11 directly, agy uses notify only — neither needs the shim). --all sets all
// fields, so it is targeted on every axis.
func TestTargetMembership(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                       string
		targets                    DeployTargets
		wantClaude, wantCx, wantSh bool
	}{
		{"claude only", DeployTargets{Claude: true}, true, false, true},
		{"codex only", DeployTargets{Codex: true}, false, true, false},
		{"opencode only", DeployTargets{Opencode: true}, false, false, true},
		{"agy only", DeployTargets{Antigravity: true}, false, false, false},
		{"all", DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}, true, true, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := claudeTargeted(tt.targets); got != tt.wantClaude {
				t.Errorf("claudeTargeted=%v want %v", got, tt.wantClaude)
			}
			if got := codexTargeted(tt.targets); got != tt.wantCx {
				t.Errorf("codexTargeted=%v want %v", got, tt.wantCx)
			}
			if got := shimTargeted(tt.targets); got != tt.wantSh {
				t.Errorf("shimTargeted=%v want %v", got, tt.wantSh)
			}
		})
	}
}

// TestNewDeployStateShimPreservation verifies that newDeployState only claims a
// shim when the shim is targeted, and otherwise preserves the prior remote
// state's shim (never downgrading or fabricating one). This is the deploy-state
// side of Option A: pure --codex skips shim install and must never report a
// shim it did not touch.
func TestNewDeployStateShimPreservation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "cc-clip")
	if err := os.WriteFile(bin, []byte("fake-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	prior := &shim.DeployState{ShimInstalled: true, ShimTarget: "wl-paste"}

	// pure --codex: shim NOT targeted -> preserve the prior shim untouched.
	st, err := newDeployState(bin, "v1", "xclip", true, prior, DeployTargets{Codex: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.ShimInstalled || st.ShimTarget != "wl-paste" {
		t.Fatalf("pure --codex must preserve prior shim, got Installed=%v Target=%q", st.ShimInstalled, st.ShimTarget)
	}

	// --claude: shim targeted -> reflect the fresh install (xclip).
	st2, err := newDeployState(bin, "v1", "xclip", true, prior, DeployTargets{Claude: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st2.ShimInstalled || st2.ShimTarget != "xclip" {
		t.Fatalf("claude must reflect fresh shim install, got Installed=%v Target=%q", st2.ShimInstalled, st2.ShimTarget)
	}

	// fresh host (no prior state), pure --codex: must NOT claim a shim.
	st3, err := newDeployState(bin, "v1", "xclip", true, nil, DeployTargets{Codex: true})
	if err != nil {
		t.Fatal(err)
	}
	if st3.ShimInstalled {
		t.Fatalf("pure --codex on fresh host must not claim a shim, got Installed=%v", st3.ShimInstalled)
	}
}

// TestMergeNotifyPreservesUntargetedAdapters verifies that an --opencode connect
// (neither Claude nor Codex attempted) leaves pre-existing claude-notify and
// codex-notify adapter entries untouched — opencode must not touch Claude/Codex
// notify config (design §3). Exercises applyAdapterState's !attempted branch.
func TestMergeNotifyPreservesUntargetedAdapters(t *testing.T) {
	t.Parallel()
	state := &shim.DeployState{Notify: &shim.NotifyDeployState{
		Adapters: map[shim.AdapterID]*shim.AdapterState{
			shim.AdapterClaudeNotify: {Installed: true, Source: install.SourceConfig},
			shim.AdapterCodexNotify:  {Installed: true, Source: install.SourceConfig},
		},
	}}
	mergeNotifyDeployState(state, notifyOutcome{
		hookScriptInstalled: true,
		claudeAttempted:     false,
		codexAttempted:      false,
		healthVerified:      true,
	})
	if a := state.Notify.Adapters[shim.AdapterClaudeNotify]; a == nil || !a.Installed {
		t.Fatal("opencode connect must preserve the existing claude-notify adapter")
	}
	if a := state.Notify.Adapters[shim.AdapterCodexNotify]; a == nil || !a.Installed {
		t.Fatal("opencode connect must preserve the existing codex-notify adapter")
	}
}

// TestMergeNotifyAsymmetricPreservation verifies the per-adapter cross-run
// anti-downgrade invariant (constraint §4): a --codex run (Claude not attempted,
// Codex attempted) must leave a prior claude-notify adapter untouched while
// wiring codex-notify, and a --claude run must leave a prior codex-notify entry
// untouched while wiring claude-notify. applyAdapterState handles each adapter
// independently, so the two axes never interfere.
func TestMergeNotifyAsymmetricPreservation(t *testing.T) {
	t.Parallel()

	t.Run("--codex preserves prior claude-notify and wires codex-notify", func(t *testing.T) {
		t.Parallel()
		state := &shim.DeployState{Notify: &shim.NotifyDeployState{
			Adapters: map[shim.AdapterID]*shim.AdapterState{
				shim.AdapterClaudeNotify: {Installed: true, Source: install.SourceConfig},
			},
		}}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			claudeAttempted:     false,
			codexAttempted:      true,
			codexInjected:       true,
			healthVerified:      true,
		})
		if a := state.Notify.Adapters[shim.AdapterClaudeNotify]; a == nil || !a.Installed {
			t.Fatal("--codex must preserve the existing claude-notify adapter")
		}
		if a := state.Notify.Adapters[shim.AdapterCodexNotify]; a == nil || !a.Installed {
			t.Fatal("--codex must wire the codex-notify adapter when injected")
		}
	})

	t.Run("--claude preserves prior codex-notify and wires claude-notify", func(t *testing.T) {
		t.Parallel()
		state := &shim.DeployState{Notify: &shim.NotifyDeployState{
			Adapters: map[shim.AdapterID]*shim.AdapterState{
				shim.AdapterCodexNotify: {Installed: true, Source: install.SourceConfig},
			},
		}}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			claudeAttempted:     true,
			claudeWired:         true,
			codexAttempted:      false,
			healthVerified:      true,
		})
		if a := state.Notify.Adapters[shim.AdapterCodexNotify]; a == nil || !a.Installed {
			t.Fatal("--claude must preserve the existing codex-notify adapter")
		}
		if a := state.Notify.Adapters[shim.AdapterClaudeNotify]; a == nil || !a.Installed {
			t.Fatal("--claude must wire the claude-notify adapter when wired")
		}
	})
}
