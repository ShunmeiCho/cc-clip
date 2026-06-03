package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shunmei/cc-clip/internal/install"
	"github.com/shunmei/cc-clip/internal/plugin"
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
		name                                string
		targets                             DeployTargets
		wantClaude, wantCx, wantSh, wantAgy bool
	}{
		{"claude only", DeployTargets{Claude: true}, true, false, true, false},
		{"codex only", DeployTargets{Codex: true}, false, true, false, false},
		{"opencode only", DeployTargets{Opencode: true}, false, false, true, false},
		{"agy only", DeployTargets{Antigravity: true}, false, false, false, true},
		{"all", DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}, true, true, true, true},
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
			if got := agyTargeted(tt.targets); got != tt.wantAgy {
				t.Errorf("agyTargeted=%v want %v", got, tt.wantAgy)
			}
		})
	}
}

// TestAgyAdapterIDsMatch pins the shim deploy-state AdapterID for agy-notify to
// the plugin dispatcher key: they live in separate packages but MUST be the same
// string, or connect would record deploy-state under a key the runner never uses.
func TestAgyAdapterIDsMatch(t *testing.T) {
	t.Parallel()
	if string(shim.AdapterAntigravityNotify) != string(plugin.AdapterAntigravityNotify) {
		t.Fatalf("shim.AdapterAntigravityNotify=%q != plugin.AdapterAntigravityNotify=%q",
			shim.AdapterAntigravityNotify, plugin.AdapterAntigravityNotify)
	}
	if shim.AdapterAntigravityNotify != "agy-notify" {
		t.Fatalf("AdapterAntigravityNotify=%q want \"agy-notify\"", shim.AdapterAntigravityNotify)
	}
}

// TestOpencodeNotifyAdapterIDsMatch pins the shim deploy-state AdapterID for
// opencode-notify to the plugin dispatcher key: they live in separate packages
// but MUST be the same string, or connect would record deploy-state under a key
// the runner never uses.
func TestOpencodeNotifyAdapterIDsMatch(t *testing.T) {
	t.Parallel()
	if string(shim.AdapterOpencodeNotify) != string(plugin.AdapterOpencodeNotify) {
		t.Fatalf("shim.AdapterOpencodeNotify=%q != plugin.AdapterOpencodeNotify=%q",
			shim.AdapterOpencodeNotify, plugin.AdapterOpencodeNotify)
	}
	if shim.AdapterOpencodeNotify != "opencode-notify" {
		t.Fatalf("AdapterOpencodeNotify=%q want \"opencode-notify\"", shim.AdapterOpencodeNotify)
	}
}

// TestOpencodeNotifyTargeted verifies the opencode notify gate predicate fires
// for an --opencode run and stays off for an unrelated (--claude-only) run.
// opencode's clipboard already works via the shim; this predicate gates ONLY the
// notify plugin.
func TestOpencodeNotifyTargeted(t *testing.T) {
	t.Parallel()
	if !opencodeNotifyTargeted(DeployTargets{Opencode: true}) {
		t.Fatal("opencodeNotifyTargeted must be true for DeployTargets{Opencode:true}")
	}
	if opencodeNotifyTargeted(DeployTargets{Claude: true}) {
		t.Fatal("opencodeNotifyTargeted must be false for DeployTargets{Claude:true} only")
	}
}

// TestBuildNotifyAdaptersOpencodeRow asserts buildNotifyAdapters() (the extracted
// detect-install table seam) registers an opencode-notify row whose gate
// predicate is true under --opencode and false under --claude-only. Behavior test
// on the seam — no reflection/string-matching of the predicate identity.
func TestBuildNotifyAdaptersOpencodeRow(t *testing.T) {
	t.Parallel()
	var row *detectInstallAdapter
	for i := range buildNotifyAdapters() {
		if buildNotifyAdapters()[i].id == shim.AdapterOpencodeNotify {
			a := buildNotifyAdapters()[i]
			row = &a
			break
		}
	}
	if row == nil {
		t.Fatal("buildNotifyAdapters() must contain an opencode-notify row")
	}
	if !row.targeted(DeployTargets{Opencode: true}) {
		t.Fatal("opencode-notify row predicate must be true for DeployTargets{Opencode:true}")
	}
	if row.targeted(DeployTargets{Claude: true}) {
		t.Fatal("opencode-notify row predicate must be false for DeployTargets{Claude:true} only")
	}
}

// TestMergeNotifyDeployStateOpencodeAdapter verifies the opencode-notify adapter
// is recorded in the per-adapter map with Verified=false (a successful plugin
// drop proves only that the file was written, NOT that session.idle fires) when
// attempted+installed, and an existing opencode-notify entry is PRESERVED
// untouched when opencode is NOT attempted this connect (5.3c).
func TestMergeNotifyDeployStateOpencodeAdapter(t *testing.T) {
	t.Run("installed -> present, Verified=false, Source=config", func(t *testing.T) {
		state := &shim.DeployState{Notify: nil}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			opencodeAttempted:   true,
			opencodeInstalled:   true,
		})
		a := state.Notify.Adapters[shim.AdapterOpencodeNotify]
		if a == nil || !a.Installed {
			t.Fatalf("opencode adapter must be Installed=true: %+v", a)
		}
		if a.Verified {
			t.Fatalf("opencode adapter Verified must be false (drop != session.idle proof): %+v", a)
		}
		if a.Source != install.SourceConfig {
			t.Fatalf("opencode adapter Source = %q, want %q", a.Source, install.SourceConfig)
		}
	})
	t.Run("not attempted -> preserves existing opencode entry", func(t *testing.T) {
		state := &shim.DeployState{
			Notify: &shim.NotifyDeployState{
				Adapters: map[shim.AdapterID]*shim.AdapterState{
					shim.AdapterOpencodeNotify: {Installed: true, Verified: false, Source: install.SourceConfig},
				},
			},
		}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			claudeAttempted:     true,
			claudeWired:         true,
			opencodeAttempted:   false,
		})
		if a := state.Notify.Adapters[shim.AdapterOpencodeNotify]; a == nil || !a.Installed {
			t.Fatalf("un-targeted connect must preserve existing opencode adapter: %+v", a)
		}
	})
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

// TestDetectInstallAdapterRun verifies the detect-install flow for one notify
// adapter — the seam future notify CLIs (copilot, cursor) plug into. An
// untargeted adapter is skipped (attempted=false); a targeted adapter is
// attempted=true regardless of detection, and installed=true only on a
// successful install. detect/install are stubbed so no real session is needed.
func TestDetectInstallAdapterRun(t *testing.T) {
	t.Parallel()
	targeted := DeployTargets{Antigravity: true}
	untargeted := DeployTargets{Claude: true} // agy not among targets
	ok := func(shim.RemoteExecutor) (bool, error) { return true, nil }
	notFound := func(shim.RemoteExecutor) (bool, error) { return false, nil }
	probeErr := func(shim.RemoteExecutor) (bool, error) { return false, fmt.Errorf("probe failed") }
	installOK := func(shim.RemoteExecutor, int) error { return nil }
	installErr := func(shim.RemoteExecutor, int) error { return fmt.Errorf("install failed") }

	tests := []struct {
		name          string
		targets       DeployTargets
		detect        func(shim.RemoteExecutor) (bool, error)
		install       func(shim.RemoteExecutor, int) error
		wantAttempted bool
		wantInstalled bool
	}{
		{"not targeted -> skipped", untargeted, ok, installOK, false, false},
		{"targeted, detected, installed", targeted, ok, installOK, true, true},
		{"targeted, not detected", targeted, notFound, installOK, true, false},
		{"targeted, detect error", targeted, probeErr, installOK, true, false},
		{"targeted, install error", targeted, ok, installErr, true, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := detectInstallAdapter{
				id:       shim.AdapterAntigravityNotify,
				label:    "Antigravity",
				step:     "N5.5",
				fileNote: "installed",
				targeted: agyTargeted,
				detect:   tt.detect,
				install:  tt.install,
			}
			got := a.run(nil, 18339, tt.targets)
			if got.attempted != tt.wantAttempted || got.installed != tt.wantInstalled {
				t.Fatalf("run() = %+v, want {attempted:%v installed:%v}", got, tt.wantAttempted, tt.wantInstalled)
			}
		})
	}
}

// TestRunDetectInstallAdaptersKeysOutcomesByID verifies the collected outcomes
// are keyed by adapter id, mixing a successful (codex) and a not-detected (agy)
// adapter in one pass.
func TestRunDetectInstallAdaptersKeysOutcomesByID(t *testing.T) {
	t.Parallel()
	adapters := []detectInstallAdapter{
		{
			id: shim.AdapterCodexNotify, label: "Codex", step: "N5", targeted: codexTargeted,
			detect:  func(shim.RemoteExecutor) (bool, error) { return true, nil },
			install: func(shim.RemoteExecutor, int) error { return nil },
		},
		{
			id: shim.AdapterAntigravityNotify, label: "Antigravity", step: "N5.5", targeted: agyTargeted,
			detect:  func(shim.RemoteExecutor) (bool, error) { return false, nil },
			install: func(shim.RemoteExecutor, int) error { return nil },
		},
	}
	out := runDetectInstallAdapters(nil, 18339, DeployTargets{Codex: true, Antigravity: true}, adapters)
	if c := out[shim.AdapterCodexNotify]; !c.attempted || !c.installed {
		t.Fatalf("codex outcome = %+v, want attempted+installed", c)
	}
	if a := out[shim.AdapterAntigravityNotify]; !a.attempted || a.installed {
		t.Fatalf("agy outcome = %+v, want attempted, not installed", a)
	}
}

// TestMergeNotifyDeployStateAgyAdapter verifies the agy-notify adapter is
// recorded in the per-adapter map with Verified=false (a successful install
// proves only that agy accepted the layout, NOT that the Stop hook fires),
// absent when not installed, and preserved when not targeted this connect.
func TestMergeNotifyDeployStateAgyAdapter(t *testing.T) {
	t.Run("installed -> present, Verified=false, Source=config", func(t *testing.T) {
		state := &shim.DeployState{Notify: nil}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			agyAttempted:        true,
			agyInstalled:        true,
		})
		a := state.Notify.Adapters[shim.AdapterAntigravityNotify]
		if a == nil || !a.Installed {
			t.Fatalf("agy adapter must be Installed=true: %+v", a)
		}
		if a.Verified {
			t.Fatalf("agy adapter Verified must be false (install != hook-fire proof): %+v", a)
		}
		if a.Source != install.SourceConfig {
			t.Fatalf("agy adapter Source = %q, want %q", a.Source, install.SourceConfig)
		}
	})
	t.Run("attempted but not installed -> absent", func(t *testing.T) {
		state := &shim.DeployState{Notify: nil}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			agyAttempted:        true,
			agyInstalled:        false,
		})
		if state.Notify.Adapters != nil {
			if _, ok := state.Notify.Adapters[shim.AdapterAntigravityNotify]; ok {
				t.Fatalf("agy adapter must be absent when not installed: %+v", state.Notify.Adapters)
			}
		}
	})
	t.Run("not attempted -> preserves existing agy entry", func(t *testing.T) {
		state := &shim.DeployState{
			Notify: &shim.NotifyDeployState{
				Adapters: map[shim.AdapterID]*shim.AdapterState{
					shim.AdapterAntigravityNotify: {Installed: true, Verified: false, Source: install.SourceConfig},
				},
			},
		}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			agyAttempted:        false,
		})
		if a := state.Notify.Adapters[shim.AdapterAntigravityNotify]; a == nil || !a.Installed {
			t.Fatalf("un-targeted connect must preserve existing agy adapter: %+v", a)
		}
	})
	// Locks the downgrade clause: a connect that targets agy but fails to install
	// must downgrade a prior installed entry (not leave it claiming installed, and
	// not drop it). Starting from nil never exercises this branch, so it is asserted
	// against a seeded entry.
	t.Run("attempted but not installed -> downgrades existing agy entry", func(t *testing.T) {
		state := &shim.DeployState{
			Notify: &shim.NotifyDeployState{
				Adapters: map[shim.AdapterID]*shim.AdapterState{
					shim.AdapterAntigravityNotify: {Installed: true, Verified: true, Source: install.SourceConfig},
				},
			},
		}
		mergeNotifyDeployState(state, notifyOutcome{
			hookScriptInstalled: true,
			agyAttempted:        true,
			agyInstalled:        false,
		})
		a := state.Notify.Adapters[shim.AdapterAntigravityNotify]
		if a == nil {
			t.Fatal("downgrade must keep the entry, not drop it")
		}
		if a.Installed || a.Verified {
			t.Fatalf("stale agy adapter must be downgraded to Installed=false, Verified=false: %+v", a)
		}
	})
}
