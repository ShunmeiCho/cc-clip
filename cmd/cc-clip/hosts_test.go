package main

import (
	"path/filepath"
	"testing"

	"github.com/shunmei/cc-clip/internal/hosts"
)

// withRegistryOverride points the host registry at a per-test file so these
// tests never touch the real ~/.cache/cc-clip registry.
func withRegistryOverride(t *testing.T) {
	t.Helper()
	old := hosts.RegistryPathOverride
	hosts.RegistryPathOverride = filepath.Join(t.TempDir(), "hosts.json")
	t.Cleanup(func() { hosts.RegistryPathOverride = old })
}

func TestRecordHostConnectRoundTrip(t *testing.T) {
	withRegistryOverride(t)

	recordHostConnect("venus", "v0.9.3", false)

	reg, err := hosts.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries := reg.Sorted()
	if len(entries) != 1 || entries[0].Host != "venus" {
		t.Fatalf("entries = %+v, want exactly venus", entries)
	}
	if entries[0].LastDeployedVersion != "v0.9.3" {
		t.Fatalf("version = %q, want v0.9.3", entries[0].LastDeployedVersion)
	}
	if entries[0].Codex {
		t.Fatal("codex must be false for a plain connect")
	}
}

// TestRecordHostConnectCodexIsSticky pins the documented registry contract: a
// later non-Codex connect must not downgrade a previously recorded Codex=true
// entry, or a plain reconnect would silently drop the host from Codex-aware
// update reminders.
func TestRecordHostConnectCodexIsSticky(t *testing.T) {
	withRegistryOverride(t)

	recordHostConnect("venus", "v0.9.2", true)
	recordHostConnect("venus", "v0.9.3", false)

	reg, _ := hosts.Load()
	entries := reg.Sorted()
	if len(entries) != 1 || !entries[0].Codex {
		t.Fatalf("codex flag must stay sticky across plain connects: %+v", entries)
	}
	if entries[0].LastDeployedVersion != "v0.9.3" {
		t.Fatalf("version must still update: %q", entries[0].LastDeployedVersion)
	}
}

func TestRecordHostConnectIgnoresEmptyHost(t *testing.T) {
	withRegistryOverride(t)

	recordHostConnect("", "v0.9.3", false)

	reg, _ := hosts.Load()
	if got := len(reg.Sorted()); got != 0 {
		t.Fatalf("empty host must not be recorded, got %d entries", got)
	}
}

func TestClearHostCodex(t *testing.T) {
	withRegistryOverride(t)

	recordHostConnect("venus", "v0.9.3", true)
	clearHostCodex("venus")

	reg, _ := hosts.Load()
	entries := reg.Sorted()
	if len(entries) != 1 || entries[0].Codex {
		t.Fatalf("clearHostCodex must flip the sticky flag off: %+v", entries)
	}

	// Unknown host and empty host are both silent no-ops.
	clearHostCodex("unknown-host")
	clearHostCodex("")
}

func TestPrintPerHostRedeployReminders(t *testing.T) {
	withRegistryOverride(t)

	if printPerHostRedeployReminders() {
		t.Fatal("empty registry must report false so the caller falls back to the generic reminder")
	}

	recordHostConnect("venus", "v0.9.3", false)
	if !printPerHostRedeployReminders() {
		t.Fatal("non-empty registry must report true")
	}
}

func TestRegistryVersionOrEmpty(t *testing.T) {
	// main.version is "dev" in test builds; the registry must receive "" so a
	// dev build never pollutes recorded release versions.
	if got := registryVersionOrEmpty(); got != normalizeVersion(version) {
		t.Fatalf("registryVersionOrEmpty() = %q, want normalizeVersion(version) = %q", got, normalizeVersion(version))
	}
}

// TestHostsListAndForget covers the happy paths of the two subcommands (the
// error paths call os.Exit and stay out of unit-test reach by design).
func TestHostsListAndForget(t *testing.T) {
	withRegistryOverride(t)

	hostsList() // empty registry prints the "no known hosts" hint

	recordHostConnect("venus", "v0.9.3", true)
	hostsList() // with an entry prints the table

	hostsForget([]string{"venus"})
	reg, _ := hosts.Load()
	if got := len(reg.Sorted()); got != 0 {
		t.Fatalf("forget must remove the entry, %d left", got)
	}
}
