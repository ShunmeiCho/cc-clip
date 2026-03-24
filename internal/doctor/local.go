package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
)

type CheckResult struct {
	Name    string
	OK      bool
	Message string
}

func RunLocal(port int) []CheckResult {
	var results []CheckResult

	// Check daemon
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := tunnel.Probe(addr, 500*time.Millisecond); err != nil {
		results = append(results, CheckResult{"daemon", false, fmt.Sprintf("not running on :%d", port)})
	} else {
		results = append(results, CheckResult{"daemon", true, fmt.Sprintf("running on :%d", port)})
	}

	// Check clipboard tool (platform-specific)
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pngpaste"); err != nil {
			results = append(results, CheckResult{"clipboard", false, "pngpaste not found (brew install pngpaste)"})
		} else {
			results = append(results, CheckResult{"clipboard", true, "pngpaste available"})
		}
	case "linux":
		foundXclip := exec.Command("which", "xclip").Run() == nil
		foundWlPaste := exec.Command("which", "wl-paste").Run() == nil
		if foundXclip || foundWlPaste {
			tool := "xclip"
			if foundWlPaste {
				tool = "wl-paste"
			}
			results = append(results, CheckResult{"clipboard", true, fmt.Sprintf("%s available", tool)})
		} else {
			results = append(results, CheckResult{"clipboard", false, "xclip or wl-paste not found"})
		}
	case "windows":
		if _, err := exec.LookPath("powershell"); err != nil {
			results = append(results, CheckResult{"clipboard", false, "PowerShell not found"})
		} else {
			results = append(results, CheckResult{"clipboard", true, "PowerShell available (built-in clipboard)"})
		}
	default:
		results = append(results, CheckResult{"clipboard", false, fmt.Sprintf("unsupported platform: %s", runtime.GOOS)})
	}

	// Check token
	tok, err := token.ReadTokenFile()
	if err != nil {
		results = append(results, CheckResult{"token", false, "not found"})
	} else {
		results = append(results, CheckResult{"token", true, fmt.Sprintf("present (%d chars)", len(tok))})
	}

	// Check token expiry
	results = append(results, checkTokenExpiry()...)

	// Check system service (macOS/Windows)
	if runtime.GOOS == "darwin" {
		results = append(results, checkLaunchdService()...)
	} else if runtime.GOOS == "windows" {
		results = append(results, checkScheduledTaskService()...)
	}

	return results
}

// checkTokenExpiry checks if the stored token has expired by parsing the
// explicit expiry timestamp from the token file.
func checkTokenExpiry() []CheckResult {
	_, expiresAt, err := token.ReadTokenFileWithExpiry()
	if err != nil {
		// Fallback: file exists but uses old format (no expiry line).
		// Check file mtime as a rough proxy.
		dir, dirErr := token.TokenDir()
		if dirErr != nil {
			return []CheckResult{{"token-expiry", false, "cannot determine token directory"}}
		}
		path := filepath.Join(dir, "session.token")
		info, statErr := os.Stat(path)
		if statErr != nil {
			return []CheckResult{{"token-expiry", false, "token file not found"}}
		}
		age := time.Since(info.ModTime())
		return []CheckResult{{"token-expiry", false,
			fmt.Sprintf("old token format (no expiry stored), file is %s old — restart daemon to upgrade", formatDuration(age))}}
	}

	remaining := time.Until(expiresAt)
	if remaining <= 0 {
		return []CheckResult{{"token-expiry", false,
			fmt.Sprintf("token expired %s ago", formatDuration(-remaining))}}
	}

	return []CheckResult{{"token-expiry", true,
		fmt.Sprintf("token valid, expires in %s (%s)", formatDuration(remaining), expiresAt.Format(time.RFC3339))}}
}

// checkLaunchdService checks if the cc-clip launchd service is installed (macOS).
func checkLaunchdService() []CheckResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return []CheckResult{{"launchd", false, "cannot determine home directory"}}
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cc-clip.daemon.plist")
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return []CheckResult{{"launchd", false, "service not installed (run 'cc-clip service install')"}}
	}

	// Check if the job is loaded
	out, err := exec.Command("launchctl", "list").CombinedOutput()
	if err != nil {
		return []CheckResult{{"launchd", false, "cannot query launchctl"}}
	}

	if strings.Contains(string(out), "com.cc-clip.daemon") {
		return []CheckResult{{"launchd", true, "service installed and loaded"}}
	}

	return []CheckResult{{"launchd", false, "plist installed but service not loaded"}}
}

// checkScheduledTaskService checks if the cc-clip auto-start is configured (Windows).
func checkScheduledTaskService() []CheckResult {
	// Check registry Run key for auto-start entry
	cmd := exec.Command("reg.exe", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", "cc-clip-daemon")
	if err := cmd.Run(); err != nil {
		return []CheckResult{{"service", false, "auto-start not configured (run 'cc-clip service install')"}}
	}
	return []CheckResult{{"service", true, "auto-start configured (registry)"}}
}

// formatDuration formats a duration in a human-readable form.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func PrintResults(results []CheckResult) bool {
	allOK := true
	for _, r := range results {
		mark := "pass"
		if !r.OK {
			mark = "FAIL"
			allOK = false
		}
		fmt.Printf("  %-14s [%s] %s\n", r.Name+":", mark, r.Message)
	}
	return allOK
}
