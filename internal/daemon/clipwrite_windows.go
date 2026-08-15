//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/shunmei/cc-clip/internal/win32"
)

type windowsTextWriter struct{}

// NewClipboardTextWriter returns the Windows clipboard writer. It reuses the
// PowerShell SetDataObject($true) + OleFlushClipboard approach proven by the
// send/hotkey path: a short-lived process must explicitly ask WinForms to
// leave the data on the clipboard after it exits, or Windows destroys the
// data with the owning window.
func NewClipboardTextWriter() ClipboardTextWriter {
	return &windowsTextWriter{}
}

const windowsWriteTextScript = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
if (-not ('CcClipDaemonOle' -as [type])) {
  Add-Type -TypeDefinition @"
using System.Runtime.InteropServices;
public static class CcClipDaemonOle {
    [DllImport("ole32.dll")]
    public static extern int OleFlushClipboard();
}
"@
}
[System.Windows.Forms.Clipboard]::SetDataObject($env:CC_CLIP_WRITE_TEXT, $true)
$hr = [CcClipDaemonOle]::OleFlushClipboard()
if ($hr -ne 0) { throw "OleFlushClipboard failed with HRESULT 0x$($hr.ToString('X8'))" }`

func (w *windowsTextWriter) WriteText(text string) error {
	cmd := exec.Command("powershell", "-STA", "-NoProfile", "-Command", windowsWriteTextScript)
	win32.HideConsoleWindow(cmd)
	cmd.Env = append(os.Environ(), "CC_CLIP_WRITE_TEXT="+text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set clipboard text: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
