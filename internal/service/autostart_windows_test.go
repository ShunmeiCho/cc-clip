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

func TestInstallRollbackOnStartFailure(t *testing.T) {
	originalAdd := regAdd
	originalDelete := regDelete
	originalStart := startDaemon
	t.Cleanup(func() {
		regAdd = originalAdd
		regDelete = originalDelete
		startDaemon = originalStart
	})

	regAdd = func(key, name, value string) error { return nil }
	startDaemon = func(vbs string) error { return errors.New("wscript not found") }
	regDeleteCalled := false
	regDelete = func(key, name string) error {
		regDeleteCalled = true
		return nil
	}

	err := Install(`C:\fake\cc-clip.exe`, 18339)
	if err == nil {
		t.Fatal("expected Install to fail")
	}
	if !regDeleteCalled {
		t.Fatal("expected regDelete rollback on startDaemon failure")
	}
	// VBS file should also be cleaned up.
	if _, err := os.Stat(vbsPath()); !os.IsNotExist(err) {
		t.Fatal("expected VBS file to be removed on startDaemon failure")
	}
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

// TestStatusIgnoresHostnamesContainingServe pins the substring bug: a bare
// -match 'serve' selected `cc-clip.exe hotkey myserver --run-loop`, so Uninstall
// taskkill /F'd the user's hotkey process along with the daemon.
func TestStatusIgnoresHostnamesContainingServe(t *testing.T) {
	cases := []struct {
		commandLine string
		want        bool
	}{
		{`"C:\tools\cc-clip.exe" serve --port 18339`, true},
		{`cc-clip.exe serve --port 18339`, true},
		{`cc-clip.exe hotkey myserver --run-loop`, false},
		{`cc-clip.exe hotkey --run-loop --remote-dir C:\serve`, false},
		// The match must be anchored at argv0, not free to start anywhere:
		// a file argument may legitimately contain the text "cc-clip serve".
		{`cc-clip.exe send myserver "C:\images\cc-clip serve.png"`, false},
		{`"C:\tools\cc-clip.exe" send host "C:\a\cc-clip serve.png"`, false},
		// PowerShell's -match is case-insensitive; Go must agree or Status and
		// the kill path disagree about the same process.
		{`"C:\tools\CC-CLIP.EXE" serve --port 18339`, true},
		{`C:\tools\cc-clip.exe serve`, true},
	}
	for _, tc := range cases {
		if got := serveSubcommandRE.MatchString(tc.commandLine); got != tc.want {
			t.Errorf("match(%q) = %v, want %v", tc.commandLine, got, tc.want)
		}
	}
}

// TestVBSServeArgsRoundTrip: serve flags baked into the launcher are read back,
// so setup/connect re-registration on Windows keeps --features.
func TestVBSServeArgsRoundTrip(t *testing.T) {
	vbs := generateVBS(`C:\tools\cc-clip.exe`, 18339, "--features", "image,notify")
	if got, ok := parseVBSFlag(vbs, "features"); !ok || got != "image,notify" {
		t.Fatalf("parseVBSFlag(features) = %q, %v; want image,notify, true", got, ok)
	}
	if _, ok := parseVBSFlag(generateVBS(`C:\tools\cc-clip.exe`, 18339), "features"); ok {
		t.Fatal("a launcher without --features must report no features flag")
	}
}
