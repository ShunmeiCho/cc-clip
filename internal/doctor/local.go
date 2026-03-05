package doctor

import (
	"fmt"
	"os/exec"
	"runtime"
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

	return results
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
