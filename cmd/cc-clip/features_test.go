package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/shim"
)

func TestGetFeaturesFlagOverridesEnv(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	t.Setenv("CC_CLIP_FEATURES", "all")

	os.Args = []string{"cc-clip", "serve", "--features", "image"}
	if got := getFeatures(); got != (daemon.Features{Image: true}) {
		t.Fatalf("getFeatures() = %v, want image from the flag", got)
	}
	os.Args = []string{"cc-clip", "serve", "--features=image,text-write"}
	if got := getFeatures(); got != (daemon.Features{Image: true, TextWrite: true}) {
		t.Fatalf("getFeatures() = %v, want image,text-write", got)
	}
	os.Args = []string{"cc-clip", "serve"}
	if got := getFeatures(); got != daemon.AllFeatures {
		t.Fatalf("getFeatures() = %v, want all from env", got)
	}
}

func TestFeatureServeArgs(t *testing.T) {
	if args := featureServeArgs(daemon.AllFeatures); args != nil {
		t.Fatalf("the default must add no serve args (keeps existing plists unchanged), got %q", args)
	}
	args := featureServeArgs(daemon.Features{Image: true, Notify: true})
	if len(args) != 2 || args[0] != "--features" || args[1] != "image,notify" {
		t.Fatalf("featureServeArgs(image,notify) = %q", args)
	}
	if args := featureServeArgs(daemon.Features{}); len(args) != 2 || args[1] != "none" {
		t.Fatalf("featureServeArgs(none) = %q", args)
	}
}

func TestSetupHostSkipsFeaturesValue(t *testing.T) {
	host, err := hostFromArgs([]string{"--features", "image", "venus"})
	if err != nil || host != "venus" {
		t.Fatalf("hostFromArgs = %q, %v; want venus (the --features value is not a host)", host, err)
	}
}

const testManagedClaudeSettings = `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"env CC_CLIP_MANAGED=1 cc-clip plugin run claude-notify"}]}]}}`

func newTestSessionWithClaudeHooks(t *testing.T) *testRemoteSession {
	t.Helper()
	s := &testRemoteSession{home: t.TempDir()}
	path := filepath.Join(s.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testManagedClaudeSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	return s
}

func noHooksMarkerExists(home string) bool {
	_, err := os.Stat(filepath.Join(home, ".cache", "cc-clip", "no-hooks"))
	return err == nil
}

func TestRemoveClaudeHooksIfRequested(t *testing.T) {
	s := newTestSessionWithClaudeHooks(t)
	removeClaudeHooksIfRequested(s, 18339, connectOpts{noHooks: true, targets: DeployTargets{Claude: true}})
	if got := readTestClaudeSettings(t, s.home); strings.Contains(got, "CC_CLIP_MANAGED") {
		t.Fatalf("managed hook should be removed:\n%s", got)
	}

	s = newTestSessionWithClaudeHooks(t)
	removeClaudeHooksIfRequested(s, 18339, connectOpts{targets: DeployTargets{Claude: true}})
	if got := readTestClaudeSettings(t, s.home); !strings.Contains(got, "CC_CLIP_MANAGED") {
		t.Fatal("without --no-hooks the settings must be left alone")
	}
}

// TestRemoveNotifyHooks: when the daemon serves no notifications, connect
// removes the managed hooks without the persistent opt-out marker (so turning
// notify back on restores them) and stops claiming them in the deploy state.
func TestRemoveNotifyHooks(t *testing.T) {
	s := newTestSessionWithClaudeHooks(t)
	state := &shim.DeployState{Notify: &shim.NotifyDeployState{
		Enabled:        true,
		HealthVerified: true,
		Adapters:       map[shim.AdapterID]*shim.AdapterState{shim.AdapterClaudeNotify: {Installed: true}},
	}}
	removeNotifyHooks(s, state, connectOpts{targets: DeployTargets{Claude: true}})

	if got := readTestClaudeSettings(t, s.home); strings.Contains(got, "CC_CLIP_MANAGED") {
		t.Fatalf("managed hook should be removed:\n%s", got)
	}
	if noHooksMarkerExists(s.home) {
		t.Fatal("notify off must not write the persistent no-hooks marker")
	}
	if state.Notify.Enabled || state.Notify.HealthVerified {
		t.Fatalf("deploy state still claims notifications: %+v", state.Notify)
	}
	if a := state.Notify.Adapters[shim.AdapterClaudeNotify]; a != nil && a.Installed {
		t.Fatal("deploy state still claims the claude-notify adapter")
	}

	s = newTestSessionWithClaudeHooks(t)
	removeNotifyHooks(s, nil, connectOpts{noHooks: true, targets: DeployTargets{Claude: true}})
	if !noHooksMarkerExists(s.home) {
		t.Fatal("an explicit --no-hooks must still write the marker")
	}

	s = newTestSessionWithClaudeHooks(t)
	removeNotifyHooks(s, nil, connectOpts{targets: DeployTargets{Codex: true}})
	if got := readTestClaudeSettings(t, s.home); !strings.Contains(got, "CC_CLIP_MANAGED") {
		t.Fatal("an untargeted Claude must be left alone")
	}
}
