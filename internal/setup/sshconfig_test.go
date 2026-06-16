package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureSSHConfig_NewHostBeforeHostStar(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host *\n    ServerAliveInterval 30\n"
	os.WriteFile(configPath, []byte(initial), 0644)

	changes, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	s := string(content)

	if !strings.Contains(s, "Host myserver") {
		t.Fatal("Host myserver block not created")
	}

	myIdx := strings.Index(s, "Host myserver")
	starIdx := strings.Index(s, "Host *")
	if myIdx >= starIdx {
		t.Fatalf("Host myserver (%d) should come before Host * (%d)", myIdx, starIdx)
	}

	if !strings.Contains(s, "RemoteForward 18339 127.0.0.1:18339") {
		t.Fatal("RemoteForward not added")
	}
	if !strings.Contains(s, "ControlMaster no") {
		t.Fatal("ControlMaster no not added")
	}
	if !strings.Contains(s, "ControlPath none") {
		t.Fatal("ControlPath none not added")
	}

	if len(changes) != 1 || changes[0].Action != "created" {
		t.Fatalf("expected 1 created change, got %v", changes)
	}
}

func TestEnsureSSHConfig_ExistingHostAddMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    HostName 10.0.0.1\n    User admin\n\nHost *\n    ServerAliveInterval 30\n"
	os.WriteFile(configPath, []byte(initial), 0644)

	changes, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	s := string(content)

	if !strings.Contains(s, "RemoteForward 18339 127.0.0.1:18339") {
		t.Fatal("RemoteForward not added")
	}
	if !strings.Contains(s, "ControlMaster no") {
		t.Fatal("ControlMaster no not added")
	}

	addedCount := 0
	for _, c := range changes {
		if c.Action == "added" {
			addedCount++
		}
	}
	if addedCount != 3 {
		t.Fatalf("expected 3 added changes, got %d from %v", addedCount, changes)
	}
}

func TestEnsureSSHConfig_AlreadyConfigured(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    RemoteForward 18339 127.0.0.1:18339\n    ControlMaster no\n    ControlPath none\n"
	os.WriteFile(configPath, []byte(initial), 0644)

	changes, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range changes {
		if c.Action != "ok" {
			t.Fatalf("expected all ok, got %v", changes)
		}
	}

	backupPath := configPath + ".cc-clip-backup"
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatal("backup should not be created when no changes needed")
	}
}

func TestEnsureSSHConfig_RemoteForwardRequiresExactValue(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    RemoteForward 118339 127.0.0.1:118339\n    ControlMaster no\n    ControlPath none\n"
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	changes, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	s := string(content)
	if !strings.Contains(s, "RemoteForward 18339 127.0.0.1:18339") {
		t.Fatalf("exact RemoteForward was not added:\n%s", s)
	}
	if strings.Count(s, "RemoteForward") != 2 {
		t.Fatalf("expected original and exact RemoteForward lines, got:\n%s", s)
	}
	foundAdded := false
	for _, c := range changes {
		if c.Action == "added" && c.Detail == "RemoteForward 18339 127.0.0.1:18339" {
			foundAdded = true
		}
	}
	if !foundAdded {
		t.Fatalf("expected added RemoteForward change, got %v", changes)
	}
}

func TestEnsureSSHConfig_RemoteForwardAcceptsLoopbackHostForms(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"ipv4", "18339 127.0.0.1:18339"},
		{"localhost", "18339 localhost:18339"},
		{"bracketed_ipv4", "18339 [127.0.0.1]:18339"},
		{"bracketed_ipv6", "18339 [::1]:18339"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config")
			initial := "Host myserver\n    RemoteForward " + tt.value + "\n    ControlMaster no\n    ControlPath none\n"
			if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
				t.Fatal(err)
			}

			changes, err := ensureSSHConfigAt(configPath, "myserver", 18339)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			content, _ := os.ReadFile(configPath)
			if strings.Count(string(content), "RemoteForward") != 1 {
				t.Fatalf("equivalent RemoteForward should not be duplicated:\n%s", string(content))
			}
			for _, c := range changes {
				if c.Detail == "RemoteForward 18339 127.0.0.1:18339" && c.Action != "ok" {
					t.Fatalf("expected RemoteForward to be ok for %q, got %v", tt.value, changes)
				}
			}
		})
	}
}

func TestEnsureSSHConfig_NoFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	changes, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	if !strings.Contains(string(content), "Host myserver") {
		t.Fatal("Host block not created")
	}

	if len(changes) != 1 || changes[0].Action != "created" {
		t.Fatalf("expected 1 created change, got %v", changes)
	}
}

func TestEnsureSSHConfig_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    HostName 10.0.0.1\n"
	os.WriteFile(configPath, []byte(initial), 0644)

	_, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	backupContent, err := os.ReadFile(configPath + ".cc-clip-backup")
	if err != nil {
		t.Fatal("backup not created")
	}
	if string(backupContent) != initial {
		t.Fatal("backup content doesn't match original")
	}
}

func TestEnsureSSHConfig_BackupFailureAbortsBeforeMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory chmod write-denial is POSIX-specific")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    HostName 10.0.0.1\n"
	if err := os.WriteFile(configPath, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	// Make the directory read+execute but not writable, so creating the
	// backup file inside it fails. The live config already exists, but
	// os.WriteFile to overwrite it would also be blocked — the point of the
	// fix is that we abort on the backup error BEFORE attempting the live
	// overwrite, so the original content survives intact.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700) // restore so t.TempDir cleanup can remove it

	_, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err == nil {
		t.Fatal("expected error when backup cannot be written, got nil")
	}

	// Restore writability to read the live config back.
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("cannot read config after failed backup: %v", readErr)
	}
	if string(content) != initial {
		t.Fatalf("live config was mutated despite backup failure:\ngot  %q\nwant %q", string(content), initial)
	}
}

func TestEnsureSSHConfig_BackupModeIs0600(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    HostName 10.0.0.1\n"
	if err := os.WriteFile(configPath, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureSSHConfigAt(configPath, "myserver", 18339); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(configPath + ".cc-clip-backup")
	if err != nil {
		t.Fatalf("backup not created: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		perm := info.Mode().Perm()
		t.Fatalf("backup mode = %o, want 0600", perm)
	}
}

func TestEnsureSSHConfig_PreservesExistingDirectives(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	initial := "Host myserver\n    HostName 10.0.0.1\n    User admin\n    Port 2222\n    IdentityFile ~/.ssh/id_rsa\n\nHost *\n    ServerAliveInterval 30\n"
	os.WriteFile(configPath, []byte(initial), 0644)

	_, err := ensureSSHConfigAt(configPath, "myserver", 18339)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	s := string(content)

	// Original directives preserved
	if !strings.Contains(s, "HostName 10.0.0.1") {
		t.Fatal("HostName lost")
	}
	if !strings.Contains(s, "User admin") {
		t.Fatal("User lost")
	}
	if !strings.Contains(s, "Port 2222") {
		t.Fatal("Port lost")
	}

	// Host * still at the end
	myIdx := strings.Index(s, "Host myserver")
	starIdx := strings.Index(s, "Host *")
	if myIdx >= starIdx {
		t.Fatal("Host myserver should come before Host *")
	}
}
