//go:build windows

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	registryKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	registryValue = "cc-clip-daemon"
)

// PlistPath returns the registry key path on Windows (for display compatibility).
func PlistPath() string {
	return registryKey + `\` + registryValue
}

// logPath returns the path for daemon log output on Windows.
func logPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".cache", "cc-clip", "cc-clip.log")
	}
	return filepath.Join(cacheDir, "cc-clip", "cc-clip.log")
}

// vbsPath returns the path to the VBScript launcher that starts cc-clip invisibly.
func vbsPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".cache", "cc-clip", "start-daemon.vbs")
	}
	return filepath.Join(cacheDir, "cc-clip", "start-daemon.vbs")
}

// generateVBS creates a VBScript that launches cc-clip serve with no visible window.
func generateVBS(binaryPath string, port int) string {
	logFile := logPath()
	return fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
WshShell.Run "cmd.exe /c """"%s"" serve --port %d >> ""%s"" 2>&1""", 0, False
`, binaryPath, port, logFile)
}

// regAdd adds a registry value. Overridable for testing.
var regAdd = func(key, name, value string) error {
	cmd := exec.Command("reg.exe", "add", key, "/v", name, "/t", "REG_SZ", "/d", value, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg add failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// regDelete deletes a registry value. Overridable for testing.
var regDelete = func(key, name string) error {
	cmd := exec.Command("reg.exe", "delete", key, "/v", name, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg delete failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// regQuery queries a registry value. Overridable for testing.
var regQuery = func(key, name string) (string, error) {
	cmd := exec.Command("reg.exe", "query", key, "/v", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reg query failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Install registers cc-clip daemon for auto-start at user logon via the registry.
// Uses a VBScript wrapper to launch the daemon with no visible window.
func Install(binaryPath string, port int) error {
	// Ensure directories exist
	logDir := filepath.Dir(logPath())
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("cannot create log directory: %w", err)
	}

	vbs := vbsPath()
	vbsDir := filepath.Dir(vbs)
	if err := os.MkdirAll(vbsDir, 0755); err != nil {
		return fmt.Errorf("cannot create VBS directory: %w", err)
	}

	// Write VBScript launcher
	content := generateVBS(binaryPath, port)
	if err := os.WriteFile(vbs, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write VBS launcher: %w", err)
	}

	// Register in HKCU\...\Run — runs wscript.exe with the VBS at logon (no window)
	regValue := fmt.Sprintf(`wscript.exe "%s"`, vbs)
	if err := regAdd(registryKey, registryValue, regValue); err != nil {
		os.Remove(vbs)
		return err
	}

	// Start the daemon immediately (no window)
	startCmd := exec.Command("wscript.exe", vbs)
	if err := startCmd.Start(); err != nil {
		return fmt.Errorf("registry entry created but failed to start daemon: %w", err)
	}

	return nil
}

// Uninstall removes the auto-start registry entry and VBScript launcher.
func Uninstall() error {
	// Remove registry entry (ignore errors if not found)
	_ = regDelete(registryKey, registryValue)

	// Remove VBScript launcher
	os.Remove(vbsPath())

	return nil
}

// Status checks if the auto-start registry entry exists and the daemon process is running.
func Status() (bool, error) {
	_, err := regQuery(registryKey, registryValue)
	if err != nil {
		return false, fmt.Errorf("not installed")
	}
	// Registry entry exists — check if process is actually running
	// by looking for cc-clip serve in the process list
	cmd := exec.Command("wmic", "process", "where",
		"name='cc-clip.exe'", "get", "CommandLine", "/format:list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil // Registry entry exists but daemon not running
	}
	if strings.Contains(string(out), "serve") {
		return true, nil
	}
	return false, nil
}
