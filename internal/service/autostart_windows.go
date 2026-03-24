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

// daemonStopFilePath returns the path to the daemon stop sentinel file.
func daemonStopFilePath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "cc-clip", "daemon.stop")
}

// generateVBS creates a VBScript that launches cc-clip serve with no visible window.
// Uses a restart loop with a stop-file sentinel, matching the hotkey VBS pattern
// and providing KeepAlive-like reliability (matching macOS launchd behavior).
func generateVBS(binaryPath string, port int) string {
	logFile := logPath()
	stopFile := daemonStopFilePath()
	return fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
Do
  If fso.FileExists("%s") Then
    fso.DeleteFile "%s", True
    Exit Do
  End If
  WshShell.Run "cmd.exe /c """"%s"" serve --port %d >> ""%s"" 2>&1""", 0, True
  If fso.FileExists("%s") Then
    fso.DeleteFile "%s", True
    Exit Do
  End If
  WScript.Sleep 5000
Loop
`, stopFile, stopFile, binaryPath, port, logFile, stopFile, stopFile)
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
	// Remove stale stop sentinel from a previous Uninstall, so the VBS loop doesn't exit immediately.
	os.Remove(daemonStopFilePath())

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
	if err := startDaemon(vbs); err != nil {
		return err
	}

	return nil
}

// startDaemon launches the daemon via wscript.exe. Overridable for testing.
var startDaemon = func(vbs string) error {
	cmd := exec.Command("wscript.exe", vbs)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("registry entry created but failed to start daemon: %w", err)
	}
	return nil
}

// stopDaemonProcess finds and terminates any running cc-clip serve process.
// Overridable for testing.
var stopDaemonProcess = func() error {
	// Find PID of the running daemon via Get-CimInstance.
	psCmd := `Get-CimInstance Win32_Process -Filter "name='cc-clip.exe'" -ErrorAction SilentlyContinue | ` +
		`Where-Object { $_.CommandLine -match 'serve' } | Select-Object -ExpandProperty ProcessId`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// PowerShell itself failed (not in PATH, restricted execution policy, etc.).
		// This is distinct from "no process found" which returns exit 0 with empty output.
		return fmt.Errorf("failed to query daemon process: %w", err)
	}
	pidStr := strings.TrimSpace(string(out))
	if pidStr == "" {
		return nil
	}
	// taskkill each matching PID (there may be multiple lines).
	var killErrs []string
	for _, line := range strings.Split(pidStr, "\n") {
		pid := strings.TrimSpace(line)
		if pid != "" {
			if err := exec.Command("taskkill", "/PID", pid, "/F").Run(); err != nil {
				killErrs = append(killErrs, fmt.Sprintf("PID %s: %v", pid, err))
			}
		}
	}
	if len(killErrs) > 0 {
		return fmt.Errorf("failed to kill daemon: %s", strings.Join(killErrs, "; "))
	}
	return nil
}

// writeDaemonStopFile writes the sentinel that tells the VBS restart loop to exit.
func writeDaemonStopFile() {
	path := daemonStopFilePath()
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("stop"), 0644)
}

// Uninstall removes the auto-start registry entry, VBScript launcher,
// and stops the running daemon process (matching macOS launchctlUnload behavior).
func Uninstall() error {
	// Write stop sentinel so the VBS restart loop exits after we kill the daemon.
	writeDaemonStopFile()

	// Stop the running daemon process.
	// The stop sentinel above ensures the VBS loop exits even if this fails.
	// We warn (not fail) because the uninstall itself (registry + VBS removal) still succeeds,
	// and the sentinel provides a fallback for stopping the VBS restart loop.
	if err := stopDaemonProcess(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not stop daemon process: %v\n", err)
	}

	// Remove registry entry (ignore errors if not found)
	_ = regDelete(registryKey, registryValue)

	// Remove VBScript launcher
	os.Remove(vbsPath())

	return nil
}

// processChecker returns the command line output for matching processes.
// Overridable for testing.
var processChecker = func() (string, error) {
	psCmd := `(Get-CimInstance Win32_Process -Filter "name='cc-clip.exe'" -ErrorAction SilentlyContinue).CommandLine`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Status checks if the auto-start registry entry exists and the daemon process is running.
func Status() (bool, error) {
	_, err := regQuery(registryKey, registryValue)
	if err != nil {
		return false, fmt.Errorf("not installed")
	}
	// Registry entry exists — check if process is actually running
	// by looking for cc-clip serve in the process list.
	out, err := processChecker()
	if err != nil {
		return false, nil // Registry entry exists but daemon not running
	}
	if strings.Contains(out, "serve") {
		return true, nil
	}
	return false, nil
}
