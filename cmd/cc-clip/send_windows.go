//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// hiddenExec creates an exec.Cmd that won't flash a console window.
func hiddenExec(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	hideConsoleWindow(cmd)
	return cmd
}

func defaultRemoteHost() (string, bool, error) {
	cfg, ok, err := loadHotkeyConfig()
	if err != nil {
		return "", false, err
	}
	if !ok || cfg.Host == "" {
		return "", false, nil
	}
	return cfg.Host, true, nil
}

func pasteRemotePath(remotePath, imagePath string, delay time.Duration, restoreClipboard bool) error {
	if err := windowsSetClipboardText(remotePath); err != nil {
		return err
	}

	if delay > 0 {
		time.Sleep(delay)
	}

	if err := windowsSendCtrlShiftV(); err != nil {
		return err
	}

	if restoreClipboard {
		time.Sleep(150 * time.Millisecond)
		if err := windowsSetClipboardImage(imagePath); err != nil {
			return fmt.Errorf("paste succeeded but clipboard restore failed: %w", err)
		}
	}

	return nil
}

// clipboardPersistenceSnippet is prepended to every clipboard-setting
// PowerShell script. Set-Clipboard and WinForms Clipboard.SetText ultimately
// give ownership to a window owned by the short-lived PowerShell process; when
// that process exits, Windows destroys the window and the clipboard data goes
// with it. SetDataObject with $true asks WinForms to leave the data on the
// clipboard after the app exits, and OleFlushClipboard forces the OLE
// rendering path to actually commit it. Using both is belt-and-braces because
// the exact persistence behavior depends on the data format and Windows
// version.
const clipboardPersistenceSnippet = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
if (-not ('CcClipOle' -as [type])) {
  Add-Type -TypeDefinition @"
using System.Runtime.InteropServices;
public static class CcClipOle {
    [DllImport("ole32.dll")]
    public static extern int OleFlushClipboard();
}
"@
}
`

func windowsSetClipboardText(text string) error {
	script := clipboardPersistenceSnippet + `
[System.Windows.Forms.Clipboard]::SetDataObject($env:CC_CLIP_TEXT, $true)
[void][CcClipOle]::OleFlushClipboard()`
	cmd := hiddenExec("powershell", "-STA", "-NoProfile", "-Command", script)
	cmd.Env = append(os.Environ(), "CC_CLIP_TEXT="+text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set text clipboard: %s: %w", string(out), err)
	}
	return nil
}

func windowsSetClipboardImage(imagePath string) error {
	script := clipboardPersistenceSnippet + `
Add-Type -AssemblyName System.Drawing
$img = [System.Drawing.Image]::FromFile($env:CC_CLIP_IMAGE_PATH)
try {
  $data = New-Object System.Windows.Forms.DataObject
  $data.SetData([System.Windows.Forms.DataFormats]::Bitmap, $true, $img)
  [System.Windows.Forms.Clipboard]::SetDataObject($data, $true)
  [void][CcClipOle]::OleFlushClipboard()
} finally {
  $img.Dispose()
}`
	cmd := hiddenExec("powershell", "-STA", "-NoProfile", "-Command", script)
	cmd.Env = append(os.Environ(), "CC_CLIP_IMAGE_PATH="+imagePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restore image clipboard: %s: %w", string(out), err)
	}
	return nil
}

func windowsSendCtrlShiftV() error {
	script := `Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('^+v')`
	cmd := hiddenExec("powershell", "-STA", "-NoProfile", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to send Ctrl+Shift+V: %s: %w", string(out), err)
	}
	return nil
}
