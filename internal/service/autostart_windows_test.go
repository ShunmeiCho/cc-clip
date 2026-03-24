//go:build windows

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusNotInstalled(t *testing.T) {
	originalQuery := regQuery
	t.Cleanup(func() { regQuery = originalQuery })

	// Registry entry does not exist — Status should report not installed.
	regQuery = func(key, name string) (string, error) {
		return "", errors.New("not found")
	}

	running, err := Status()
	if err == nil {
		t.Fatal("expected error for not-installed status")
	}
	if running {
		t.Fatal("expected not running when not installed")
	}
}

func TestStatusInstalledAndRunning(t *testing.T) {
	originalQuery := regQuery
	originalChecker := processChecker
	t.Cleanup(func() {
		regQuery = originalQuery
		processChecker = originalChecker
	})

	// Registry entry exists and Get-CimInstance reports a "serve" process.
	regQuery = func(key, name string) (string, error) {
		return "wscript.exe ...", nil
	}
	processChecker = func() (string, error) {
		return `cc-clip.exe serve --port 18339`, nil
	}

	running, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !running {
		t.Fatal("expected running when process has 'serve' in command line")
	}
}

func TestStatusInstalledButNotRunning(t *testing.T) {
	originalQuery := regQuery
	originalChecker := processChecker
	t.Cleanup(func() {
		regQuery = originalQuery
		processChecker = originalChecker
	})

	// Registry entry exists but Get-CimInstance finds no matching process.
	regQuery = func(key, name string) (string, error) {
		return "wscript.exe ...", nil
	}
	processChecker = func() (string, error) {
		return "", errors.New("no process found")
	}

	running, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if running {
		t.Fatal("expected not running when process check fails")
	}
}

func TestStatusInstalledButDifferentProcess(t *testing.T) {
	originalQuery := regQuery
	originalChecker := processChecker
	t.Cleanup(func() {
		regQuery = originalQuery
		processChecker = originalChecker
	})

	// Registry entry exists, cc-clip.exe is running, but it's the hotkey
	// process, not the daemon — Status should report not running.
	regQuery = func(key, name string) (string, error) {
		return "wscript.exe ...", nil
	}
	processChecker = func() (string, error) {
		return `cc-clip.exe hotkey --run-loop`, nil
	}

	running, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if running {
		t.Fatal("expected not running when process doesn't have 'serve'")
	}
}

func TestUninstallStopsDaemonProcess(t *testing.T) {
	originalDelete := regDelete
	originalStop := stopDaemonProcess
	t.Cleanup(func() {
		regDelete = originalDelete
		stopDaemonProcess = originalStop
	})

	stopCalled := false
	stopDaemonProcess = func() error {
		stopCalled = true
		return nil
	}
	regDelete = func(key, name string) error {
		return nil
	}

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !stopCalled {
		t.Fatal("expected stopDaemonProcess to be called during Uninstall")
	}

	// Verify daemon stop sentinel was written.
	stopFile := daemonStopFilePath()
	if _, err := os.Stat(stopFile); os.IsNotExist(err) {
		t.Fatal("expected daemon stop sentinel to be written during Uninstall")
	}
	// Clean up.
	os.Remove(stopFile)
}

func TestInstallRemovesStaleDaemonStop(t *testing.T) {
	originalAdd := regAdd
	originalStart := startDaemon
	t.Cleanup(func() {
		regAdd = originalAdd
		startDaemon = originalStart
	})

	// Stub out regAdd and startDaemon so Install doesn't touch registry or launch wscript.
	regAdd = func(key, name, value string) error { return nil }
	startDaemon = func(vbs string) error { return nil }

	// Pre-create a stale daemon.stop file (simulates previous Uninstall).
	stopFile := daemonStopFilePath()
	os.MkdirAll(filepath.Dir(stopFile), 0755)
	os.WriteFile(stopFile, []byte("stop"), 0644)
	t.Cleanup(func() {
		os.Remove(stopFile)
		os.Remove(vbsPath())
	})

	if err := Install(`C:\fake\cc-clip.exe`, 18339); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := os.Stat(stopFile); !os.IsNotExist(err) {
		t.Fatal("expected Install to remove stale daemon.stop file")
	}
}

func TestUninstallSucceedsEvenWhenStopFails(t *testing.T) {
	originalDelete := regDelete
	originalStop := stopDaemonProcess
	t.Cleanup(func() {
		regDelete = originalDelete
		stopDaemonProcess = originalStop
	})

	// Simulate PowerShell failure (e.g., not in PATH, restricted policy).
	stopDaemonProcess = func() error {
		return errors.New("powershell not found")
	}
	regDelete = func(key, name string) error { return nil }

	// Uninstall should succeed (return nil) even when stopDaemonProcess fails,
	// because registry + VBS removal are the essential operations
	// and the stop sentinel provides a fallback.
	if err := Uninstall(); err != nil {
		t.Fatalf("expected Uninstall to succeed even when stopDaemonProcess fails, got: %v", err)
	}

	// Stop sentinel should still be written for the VBS loop fallback.
	stopFile := daemonStopFilePath()
	if _, statErr := os.Stat(stopFile); os.IsNotExist(statErr) {
		t.Fatal("expected daemon stop sentinel to be written even when stopDaemonProcess fails")
	}
	os.Remove(stopFile)
}

func TestGenerateVBSContainsRestartLoop(t *testing.T) {
	content := generateVBS(`C:\tools\cc-clip.exe`, 18339)

	checks := []struct {
		name     string
		contains string
	}{
		{"restart loop", "Do"},
		{"stop file check", "fso.FileExists"},
		{"serve command", "serve --port 18339"},
		{"sleep", "WScript.Sleep 5000"},
		{"loop end", "Loop"},
		{"wait for exit", ", 0, True"},
	}
	for _, check := range checks {
		if !strings.Contains(content, check.contains) {
			t.Errorf("VBS missing %s: expected to contain %q", check.name, check.contains)
		}
	}
}
