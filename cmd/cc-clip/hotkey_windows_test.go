//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStopHotkeyProcessWritesStopSentinelAndKills(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "hotkey.pid")
	stopFile := filepath.Join(tmpDir, "hotkey.stop")

	hotkeyPIDPathOverride = pidFile
	hotkeyStopFilePathOverride = stopFile
	originalCmdFunc := localProcessCommandFunc
	t.Cleanup(func() {
		hotkeyPIDPathOverride = ""
		hotkeyStopFilePathOverride = ""
		localProcessCommandFunc = originalCmdFunc
	})

	// Mock localProcessCommand so it always reports "hotkey" in the
	// command line — prevents stopHotkeyProcess from refusing to kill.
	localProcessCommandFunc = func(pid int) (string, error) {
		return "cc-clip.exe hotkey --run-loop", nil
	}

	// Start a real child process that stopHotkeyProcess can kill.
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep 60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start child process: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		t.Fatalf("write PID file: %v", err)
	}

	stopHotkeyProcess()

	// Stop sentinel must exist — this is what prevents the VBS loop from respawning.
	if _, err := os.Stat(stopFile); os.IsNotExist(err) {
		t.Fatal("expected stop sentinel file to be created, but it does not exist")
	}
	// PID file must be cleaned up.
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("expected PID file to be removed after stop")
	}
}

func TestStopHotkeyProcessWritesSentinelEvenWhenNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "hotkey.pid")
	stopFile := filepath.Join(tmpDir, "hotkey.stop")

	hotkeyPIDPathOverride = pidFile
	hotkeyStopFilePathOverride = stopFile
	originalCmdFunc := localProcessCommandFunc
	t.Cleanup(func() {
		hotkeyPIDPathOverride = ""
		hotkeyStopFilePathOverride = ""
		localProcessCommandFunc = originalCmdFunc
	})

	localProcessCommandFunc = func(pid int) (string, error) {
		return "cc-clip.exe hotkey --run-loop", nil
	}

	// No PID file exists — hotkey process may have crashed but the VBS
	// autostart loop could still be running. The sentinel must be written
	// unconditionally so the VBS loop exits on its next iteration.
	stopHotkeyProcess()

	if _, err := os.Stat(stopFile); os.IsNotExist(err) {
		t.Fatal("expected stop sentinel file even when hotkey process is not running")
	}
}

func TestParseHotkeyAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"default", "alt+shift+v", "alt+shift+v"},
		{"case insensitive", "ALT+SHIFT+V", "alt+shift+v"},
		{"ctrl alt other key", "ctrl+alt+p", "ctrl+alt+p"},
		{"win modifier", "win+shift+v", "shift+win+v"},
		{"function key", "ctrl+f12", "ctrl+f12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHotkey(tc.in)
			if err != nil {
				t.Fatalf("parseHotkey(%q) unexpected error: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Fatalf("parseHotkey(%q).String() = %q, want %q", tc.in, got.String(), tc.want)
			}
		})
	}
}

func TestParseHotkeyRejectsPasteConflicts(t *testing.T) {
	// These combinations are rejected because they would prevent pastes
	// from reaching the terminal:
	//   - ctrl+v is the system paste shortcut; registering it as a global
	//     hotkey would hijack every paste.
	//   - ctrl+shift+v is what windowsSendCtrlShiftV synthesizes; an
	//     identical binding would be re-caught by our own RegisterHotKey
	//     loop (hotkeyRunning guard) and the simulated keystroke would be
	//     silently swallowed.
	cases := []struct {
		name     string
		in       string
		wantFrag string
	}{
		{"ctrl+v", "ctrl+v", "system paste shortcut"},
		{"ctrl+shift+v", "ctrl+shift+v", "simulated paste keystroke"},
		{"shift+ctrl+v normalized", "shift+ctrl+v", "simulated paste keystroke"},
		{"uppercase ctrl+V", "Ctrl+V", "system paste shortcut"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseHotkey(tc.in)
			if err == nil {
				t.Fatalf("parseHotkey(%q) returned no error, want rejection", tc.in)
			}
			if !strings.Contains(err.Error(), tc.wantFrag) {
				t.Fatalf("parseHotkey(%q) error = %q, want to contain %q", tc.in, err.Error(), tc.wantFrag)
			}
		})
	}
}

func TestParseHotkeyNonVKeyNotRejected(t *testing.T) {
	// Sanity check: the conflict rule only targets the V key. Ctrl+Shift+B
	// looks structurally similar to ctrl+shift+v but must remain valid.
	if _, err := parseHotkey("ctrl+shift+b"); err != nil {
		t.Fatalf("parseHotkey(ctrl+shift+b) unexpected error: %v", err)
	}
}

func TestSaveHotkeyConfigRejectsPasteConflicts(t *testing.T) {
	tmpDir := t.TempDir()
	hotkeyConfigPathOverride = filepath.Join(tmpDir, "hotkey.json")
	t.Cleanup(func() { hotkeyConfigPathOverride = "" })

	cfg := hotkeyConfig{Host: "example", Hotkey: "ctrl+shift+v"}
	if err := saveHotkeyConfig(cfg); err == nil {
		t.Fatal("saveHotkeyConfig accepted ctrl+shift+v, want rejection")
	} else if !strings.Contains(err.Error(), "simulated paste keystroke") {
		t.Fatalf("saveHotkeyConfig error = %q, want to mention conflict reason", err.Error())
	}

	cfg.Hotkey = "ctrl+v"
	if err := saveHotkeyConfig(cfg); err == nil {
		t.Fatal("saveHotkeyConfig accepted ctrl+v, want rejection")
	}
}
