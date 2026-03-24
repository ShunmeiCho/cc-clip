//go:build windows

package daemon

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/shunmei/cc-clip/internal/win32"
)

// hiddenCmd creates an exec.Cmd that won't flash a console window.
func hiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	win32.HideConsoleWindow(cmd)
	return cmd
}

type windowsClipboard struct{}

func NewClipboardReader() ClipboardReader {
	return &windowsClipboard{}
}

func (c *windowsClipboard) Type() (ClipboardInfo, error) {
	// Use PowerShell to check clipboard format
	cmd := hiddenCmd("powershell", "-NoProfile", "-Command",
		"Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Clipboard]::ContainsImage()")
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) == "True" {
		return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
	}

	cmd = hiddenCmd("powershell", "-NoProfile", "-Command",
		"Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Clipboard]::ContainsText()")
	out, err = cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) == "True" {
		return ClipboardInfo{Type: ClipboardText}, nil
	}

	return ClipboardInfo{Type: ClipboardEmpty}, nil
}

func (c *windowsClipboard) ImageBytes() ([]byte, error) {
	// Save clipboard image to temp file via PowerShell, then read it
	cmd := hiddenCmd("powershell", "-NoProfile", "-Command", `
Add-Type -AssemblyName System.Windows.Forms
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -eq $null) { exit 1 }
$ms = New-Object System.IO.MemoryStream
$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
[System.Console]::OpenStandardOutput().Write($ms.ToArray(), 0, $ms.Length)
`)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("no image in clipboard: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("clipboard image is empty")
	}
	return out, nil
}
