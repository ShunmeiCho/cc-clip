//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const hotkeyRegistryKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
const hotkeyRegistryValue = "cc-clip-hotkey"

var hotkeyConfigPathOverride string
var hotkeyAutostartVBSPathOverride string
var hotkeyExecutablePath = os.Executable
var hotkeyEvalSymlinks = filepath.EvalSymlinks
var hotkeyRegAdd = func(key, name, value string) error {
	return hotkeyRegistryAdd(key, name, value)
}
var hotkeyRegDelete = func(key, name string) error {
	out, err := hotkeyRegistryQuery(key, name)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	return hotkeyRegistryDelete(key, name)
}
var hotkeyRegQuery = func(key, name string) (string, error) {
	return hotkeyRegistryQuery(key, name)
}

type hotkeyConfig struct {
	Host          string `json:"host"`
	RemoteDir     string `json:"remote_dir"`
	DelayMS       int    `json:"delay_ms"`
	Hotkey        string `json:"hotkey"`
	Notifications *bool  `json:"notifications,omitempty"`
}

func loadHotkeyConfig() (hotkeyConfig, bool, error) {
	path := hotkeyConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return hotkeyConfig{}, false, nil
		}
		return hotkeyConfig{}, false, fmt.Errorf("cannot read hotkey config: %w", err)
	}

	var cfg hotkeyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return hotkeyConfig{}, false, fmt.Errorf("cannot parse hotkey config: %w", err)
	}
	normalizeHotkeyConfig(&cfg)
	return cfg, true, nil
}

func saveHotkeyConfig(cfg hotkeyConfig) error {
	normalizeHotkeyConfig(&cfg)
	if cfg.Host == "" {
		return fmt.Errorf("hotkey host cannot be empty")
	}
	binding, err := parseHotkey(cfg.Hotkey)
	if err != nil {
		return err
	}
	cfg.Hotkey = binding.String()

	path := hotkeyConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("cannot create hotkey config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode hotkey config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("cannot write hotkey config: %w", err)
	}
	return nil
}

func normalizeHotkeyConfig(cfg *hotkeyConfig) {
	if cfg.RemoteDir == "" {
		cfg.RemoteDir = defaultRemoteUploadDir
	}
	if cfg.DelayMS < 0 {
		cfg.DelayMS = 150
	}
	if strings.TrimSpace(cfg.Hotkey) == "" {
		cfg.Hotkey = defaultHotkeyString
	}
	if cfg.Notifications == nil {
		v := true
		cfg.Notifications = &v
	}
}

func (cfg *hotkeyConfig) notificationsEnabled() bool {
	return cfg.Notifications == nil || *cfg.Notifications
}

func hotkeyConfigPath() string {
	if hotkeyConfigPathOverride != "" {
		return hotkeyConfigPathOverride
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		cacheDir, cacheErr := os.UserCacheDir()
		if cacheErr == nil {
			return filepath.Join(cacheDir, "cc-clip", "hotkey.json")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "cc-clip", "hotkey.json")
	}
	return filepath.Join(configDir, "cc-clip", "hotkey.json")
}

func hotkeyAutostartVBSPath() string {
	if hotkeyAutostartVBSPathOverride != "" {
		return hotkeyAutostartVBSPathOverride
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "cc-clip", "start-hotkey.vbs")
}

func hotkeyAutostartEnabled() bool {
	_, err := hotkeyRegQuery(hotkeyRegistryKey, hotkeyRegistryValue)
	return err == nil
}

func installHotkeyAutostart() error {
	exe, err := hotkeyExecutablePath()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	exe, err = hotkeyEvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	vbsPath := hotkeyAutostartVBSPath()
	if err := os.MkdirAll(filepath.Dir(vbsPath), 0755); err != nil {
		return fmt.Errorf("cannot create hotkey launcher directory: %w", err)
	}

	logFile := hotkeyLogPath()
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		return fmt.Errorf("cannot create hotkey log directory: %w", err)
	}
	stopFile := hotkeyStopFilePath()
	content := fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
Do
  If fso.FileExists("%s") Then
    fso.DeleteFile "%s", True
    Exit Do
  End If
  WshShell.Run "cmd.exe /c """"%s"" hotkey --run-loop >> ""%s"" 2>&1""", 0, True
  If fso.FileExists("%s") Then
    fso.DeleteFile "%s", True
    Exit Do
  End If
  WScript.Sleep 5000
Loop
`, stopFile, stopFile, exe, logFile, stopFile, stopFile)
	if err := os.WriteFile(vbsPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write hotkey launcher: %w", err)
	}

	regValue := fmt.Sprintf(`wscript.exe "%s"`, vbsPath)
	if err := hotkeyRegAdd(hotkeyRegistryKey, hotkeyRegistryValue, regValue); err != nil {
		_ = os.Remove(vbsPath)
		return err
	}
	return nil
}

func uninstallHotkeyAutostart() error {
	if err := hotkeyRegDelete(hotkeyRegistryKey, hotkeyRegistryValue); err != nil {
		return err
	}
	_ = os.Remove(hotkeyAutostartVBSPath())
	return nil
}

func hotkeyRegistryAdd(key, name, value string) error {
	cmd := exec.Command("reg.exe", "add", key, "/v", name, "/t", "REG_SZ", "/d", value, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg add failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func hotkeyRegistryDelete(key, name string) error {
	cmd := exec.Command("reg.exe", "delete", key, "/v", name, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg delete failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func hotkeyRegistryQuery(key, name string) (string, error) {
	cmd := exec.Command("reg.exe", "query", key, "/v", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reg query failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
