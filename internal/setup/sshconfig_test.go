package setup

import (
	"os"
	"path/filepath"
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
