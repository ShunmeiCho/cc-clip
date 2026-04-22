package hosts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// overrideRegistryPath redirects the registry to a per-test file so tests
// are isolated from the user's real ~/.cache/cc-clip/hosts.json and from
// each other.
func overrideRegistryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.json")
	prev := RegistryPathOverride
	RegistryPathOverride = path
	t.Cleanup(func() { RegistryPathOverride = prev })
	return path
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	overrideRegistryPath(t)
	r, err := Load()
	if err != nil {
		t.Fatalf("unexpected error on missing registry: %v", err)
	}
	if r == nil || r.Hosts == nil {
		t.Fatal("expected empty registry, got nil map")
	}
	if len(r.Hosts) != 0 {
		t.Fatalf("expected 0 hosts, got %d", len(r.Hosts))
	}
}

func TestLoadCorruptReturnsError(t *testing.T) {
	path := overrideRegistryPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected error on corrupt registry, got nil")
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	overrideRegistryPath(t)

	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	r.UpsertConnect("myserver", "0.6.2", false)
	r.UpsertConnect("user@venus", "0.6.2", true)
	if err := r.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(loaded.Hosts))
	}
	mys, ok := loaded.Hosts["myserver"]
	if !ok {
		t.Fatal("myserver missing after reload")
	}
	if mys.LastDeployedVersion != "0.6.2" {
		t.Errorf("myserver version = %q, want 0.6.2", mys.LastDeployedVersion)
	}
	if mys.Codex {
		t.Error("myserver should NOT have codex=true")
	}
	venus, ok := loaded.Hosts["user@venus"]
	if !ok {
		t.Fatal("user@venus missing after reload")
	}
	if !venus.Codex {
		t.Error("user@venus should have codex=true")
	}
}

// TestCodexFlagIsSticky encodes the user-visible rule: plain `connect`
// must not downgrade a Codex-enabled entry. Only ClearCodex flips it off.
func TestCodexFlagIsSticky(t *testing.T) {
	overrideRegistryPath(t)
	r := &Registry{Hosts: map[string]Entry{}}

	// First connect with Codex.
	r.UpsertConnect("venus", "0.6.2", true)
	if !r.Hosts["venus"].Codex {
		t.Fatal("expected codex=true after --codex connect")
	}

	// Subsequent plain connect must NOT clear codex.
	r.UpsertConnect("venus", "0.6.3", false)
	if !r.Hosts["venus"].Codex {
		t.Error("plain connect downgraded codex to false; flag must be sticky")
	}
	if r.Hosts["venus"].LastDeployedVersion != "0.6.3" {
		t.Error("version was not updated by plain connect")
	}

	// ClearCodex is the only flag that flips codex off.
	if !r.ClearCodex("venus") {
		t.Error("ClearCodex reported entry missing; expected true")
	}
	if r.Hosts["venus"].Codex {
		t.Error("ClearCodex failed to clear the flag")
	}
}

func TestUpsertConnectUpdatesLastConnected(t *testing.T) {
	r := &Registry{Hosts: map[string]Entry{}}
	r.UpsertConnect("a", "0.6.2", false)
	first := r.Hosts["a"].LastConnected

	// Force a distinguishable interval: real time clock has enough resolution
	// for this to differ in practice, but the test is not asserting exact
	// timing, just that the field was overwritten to something non-zero.
	time.Sleep(2 * time.Millisecond)
	r.UpsertConnect("a", "0.6.3", false)
	second := r.Hosts["a"].LastConnected

	if second.Before(first) {
		t.Error("LastConnected went backwards")
	}
	if second.Equal(time.Time{}) {
		t.Error("LastConnected is zero")
	}
}

func TestUpsertEmptyVersionPreservesExisting(t *testing.T) {
	r := &Registry{Hosts: map[string]Entry{}}
	r.UpsertConnect("a", "0.6.2", false)
	// Caller passes "" because it does not know its own version (e.g. dev
	// build). We should not erase the previously recorded version.
	r.UpsertConnect("a", "", false)
	if got := r.Hosts["a"].LastDeployedVersion; got != "0.6.2" {
		t.Errorf("version = %q, want 0.6.2 (empty version must not overwrite)", got)
	}
}

func TestForget(t *testing.T) {
	r := &Registry{Hosts: map[string]Entry{}}
	r.UpsertConnect("a", "", false)
	if !r.Forget("a") {
		t.Error("Forget on existing host returned false")
	}
	if _, ok := r.Hosts["a"]; ok {
		t.Error("entry still present after Forget")
	}
	if r.Forget("a") {
		t.Error("Forget on missing host returned true")
	}
}

func TestSortedIsStable(t *testing.T) {
	r := &Registry{Hosts: map[string]Entry{}}
	for _, h := range []string{"zeta", "alpha", "gamma", "beta"} {
		r.UpsertConnect(h, "0.6.2", false)
	}
	sorted := r.Sorted()
	want := []string{"alpha", "beta", "gamma", "zeta"}
	if len(sorted) != len(want) {
		t.Fatalf("len = %d, want %d", len(sorted), len(want))
	}
	for i, h := range want {
		if sorted[i].Host != h {
			t.Errorf("Sorted()[%d].Host = %q, want %q", i, sorted[i].Host, h)
		}
	}
}

func TestSaveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits not enforced on Windows")
	}
	path := overrideRegistryPath(t)
	r := &Registry{Hosts: map[string]Entry{}}
	r.UpsertConnect("a", "0.6.2", false)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("registry mode = %04o, want 0600", mode)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	// Write an initial registry, then write a second time. If the first
	// save left any tempfile behind, the directory would have multiple
	// hosts.json* entries; we assert the only file present is the final
	// registry.
	path := overrideRegistryPath(t)
	r := &Registry{Hosts: map[string]Entry{}}
	r.UpsertConnect("a", "0.6.2", false)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r.UpsertConnect("b", "0.6.2", false)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected 1 file after two Saves, got %v", names)
	}
}
