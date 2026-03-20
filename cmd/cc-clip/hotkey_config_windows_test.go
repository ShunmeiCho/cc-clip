//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadHotkeyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	hotkeyConfigPathOverride = filepath.Join(tmpDir, "hotkey.json")
	t.Cleanup(func() {
		hotkeyConfigPathOverride = ""
	})

	input := hotkeyConfig{
		Host:      "myserver",
		RemoteDir: "",
		DelayMS:   0,
	}
	if err := saveHotkeyConfig(input); err != nil {
		t.Fatalf("saveHotkeyConfig: %v", err)
	}

	got, ok, err := loadHotkeyConfig()
	if err != nil {
		t.Fatalf("loadHotkeyConfig: %v", err)
	}
	if !ok {
		t.Fatal("expected config to exist")
	}
	if got.Host != "myserver" {
		t.Fatalf("Host = %q, want %q", got.Host, "myserver")
	}
	if got.RemoteDir != defaultRemoteUploadDir {
		t.Fatalf("RemoteDir = %q, want %q", got.RemoteDir, defaultRemoteUploadDir)
	}
	if got.DelayMS != 0 {
		t.Fatalf("DelayMS = %d, want 0", got.DelayMS)
	}
	if got.Hotkey != defaultHotkeyString {
		t.Fatalf("Hotkey = %q, want %q", got.Hotkey, defaultHotkeyString)
	}
}

func TestDefaultRemoteHostUsesSavedHotkeyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	hotkeyConfigPathOverride = filepath.Join(tmpDir, "hotkey.json")
	t.Cleanup(func() {
		hotkeyConfigPathOverride = ""
	})

	if err := saveHotkeyConfig(hotkeyConfig{
		Host:      "saved-host",
		RemoteDir: defaultRemoteUploadDir,
		DelayMS:   150,
		Hotkey:    "ctrl+alt+v",
	}); err != nil {
		t.Fatalf("saveHotkeyConfig: %v", err)
	}

	host, ok, err := defaultRemoteHost()
	if err != nil {
		t.Fatalf("defaultRemoteHost: %v", err)
	}
	if !ok {
		t.Fatal("expected saved host to be available")
	}
	if host != "saved-host" {
		t.Fatalf("host = %q, want %q", host, "saved-host")
	}
}

func TestSaveHotkeyConfigNormalizesHotkey(t *testing.T) {
	tmpDir := t.TempDir()
	hotkeyConfigPathOverride = filepath.Join(tmpDir, "hotkey.json")
	t.Cleanup(func() {
		hotkeyConfigPathOverride = ""
	})

	if err := saveHotkeyConfig(hotkeyConfig{
		Host:      "myserver",
		RemoteDir: defaultRemoteUploadDir,
		DelayMS:   150,
		Hotkey:    "Shift+Alt+V",
	}); err != nil {
		t.Fatalf("saveHotkeyConfig: %v", err)
	}

	got, ok, err := loadHotkeyConfig()
	if err != nil {
		t.Fatalf("loadHotkeyConfig: %v", err)
	}
	if !ok {
		t.Fatal("expected config to exist")
	}
	if got.Hotkey != defaultHotkeyString {
		t.Fatalf("Hotkey = %q, want %q", got.Hotkey, defaultHotkeyString)
	}
}

func TestSaveHotkeyConfigRejectsInvalidHotkey(t *testing.T) {
	tmpDir := t.TempDir()
	hotkeyConfigPathOverride = filepath.Join(tmpDir, "hotkey.json")
	t.Cleanup(func() {
		hotkeyConfigPathOverride = ""
	})

	err := saveHotkeyConfig(hotkeyConfig{
		Host:      "myserver",
		RemoteDir: defaultRemoteUploadDir,
		DelayMS:   150,
		Hotkey:    "v",
	})
	if err == nil {
		t.Fatal("expected invalid hotkey to fail")
	}
}

func TestInstallHotkeyAutostartWritesLauncherAndRegistryEntry(t *testing.T) {
	tmpDir := t.TempDir()
	vbsPath := filepath.Join(tmpDir, "start-hotkey.vbs")
	logPath := filepath.Join(tmpDir, "hotkey.log")
	hotkeyAutostartVBSPathOverride = vbsPath
	hotkeyConfigPathOverride = filepath.Join(tmpDir, "hotkey.json")
	t.Cleanup(func() {
		hotkeyAutostartVBSPathOverride = ""
		hotkeyConfigPathOverride = ""
		hotkeyExecutablePath = os.Executable
		hotkeyEvalSymlinks = filepath.EvalSymlinks
		hotkeyRegAdd = func(key, name, value string) error {
			return hotkeyRegistryAdd(key, name, value)
		}
	})

	hotkeyExecutablePath = func() (string, error) {
		return `C:\tools\cc-clip.exe`, nil
	}
	hotkeyEvalSymlinks = func(path string) (string, error) {
		return path, nil
	}
	oldHotkeyLogPath := hotkeyLogPathFunc
	hotkeyLogPathFunc = func() string {
		return logPath
	}
	t.Cleanup(func() {
		hotkeyLogPathFunc = oldHotkeyLogPath
	})

	var regValue string
	hotkeyRegAdd = func(key, name, value string) error {
		if key != hotkeyRegistryKey {
			t.Fatalf("unexpected key: %s", key)
		}
		if name != hotkeyRegistryValue {
			t.Fatalf("unexpected name: %s", name)
		}
		regValue = value
		return nil
	}

	if err := installHotkeyAutostart(); err != nil {
		t.Fatalf("installHotkeyAutostart: %v", err)
	}

	content, err := os.ReadFile(vbsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", vbsPath, err)
	}
	text := string(content)
	if !strings.Contains(text, `hotkey --run-loop`) {
		t.Fatalf("launcher missing hotkey command: %s", text)
	}
	if !strings.Contains(text, logPath) {
		t.Fatalf("launcher missing log path %q: %s", logPath, text)
	}
	if !strings.Contains(regValue, `wscript.exe "`) {
		t.Fatalf("registry value = %q, want wscript launcher", regValue)
	}
}

func TestUninstallHotkeyAutostartRemovesLauncher(t *testing.T) {
	tmpDir := t.TempDir()
	vbsPath := filepath.Join(tmpDir, "start-hotkey.vbs")
	hotkeyAutostartVBSPathOverride = vbsPath
	t.Cleanup(func() {
		hotkeyAutostartVBSPathOverride = ""
		hotkeyRegDelete = func(key, name string) error {
			out, err := hotkeyRegistryQuery(key, name)
			if err != nil || strings.TrimSpace(out) == "" {
				return nil
			}
			return hotkeyRegistryDelete(key, name)
		}
	})

	if err := os.WriteFile(vbsPath, []byte("test"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	deleteCalled := false
	hotkeyRegDelete = func(key, name string) error {
		deleteCalled = true
		return nil
	}

	if err := uninstallHotkeyAutostart(); err != nil {
		t.Fatalf("uninstallHotkeyAutostart: %v", err)
	}
	if !deleteCalled {
		t.Fatal("expected registry delete to be called")
	}
	if _, err := os.Stat(vbsPath); !os.IsNotExist(err) {
		t.Fatalf("expected launcher to be removed, got err=%v", err)
	}
}

func TestHotkeyAutostartEnabledUsesRegistryQuery(t *testing.T) {
	hotkeyRegQuery = func(key, name string) (string, error) {
		return "present", nil
	}
	t.Cleanup(func() {
		hotkeyRegQuery = func(key, name string) (string, error) {
			return hotkeyRegistryQuery(key, name)
		}
	})

	if !hotkeyAutostartEnabled() {
		t.Fatal("expected auto-start to be enabled")
	}

	hotkeyRegQuery = func(key, name string) (string, error) {
		return "", errors.New("missing")
	}
	if hotkeyAutostartEnabled() {
		t.Fatal("expected auto-start to be disabled")
	}
}

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantMod uint32
		wantKey uint32
		wantErr bool
	}{
		{name: "default", input: "", want: defaultHotkeyString, wantMod: modAlt | modShift, wantKey: 'V'},
		{name: "ctrl alt v", input: "ctrl+alt+v", want: "ctrl+alt+v", wantMod: modControl | modAlt, wantKey: 'V'},
		{name: "function key", input: "alt+f8", want: "alt+f8", wantMod: modAlt, wantKey: 0x77},
		{name: "missing modifier", input: "v", wantErr: true},
		{name: "duplicate token", input: "alt+alt+v", wantErr: true},
		{name: "multiple keys", input: "alt+v+x", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHotkey(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHotkey: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("String() = %q, want %q", got.String(), tt.want)
			}
			if got.modifiers != tt.wantMod {
				t.Fatalf("modifiers = %#x, want %#x", got.modifiers, tt.wantMod)
			}
			if got.key != tt.wantKey {
				t.Fatalf("key = %#x, want %#x", got.key, tt.wantKey)
			}
		})
	}
}
