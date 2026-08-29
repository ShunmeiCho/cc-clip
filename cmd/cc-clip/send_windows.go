//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/shunmei/cc-clip/internal/win32"
)

// systemFocusProbe is the production focusProbe. It lives here rather than
// alongside the guard logic because Windows is the only platform that can
// answer the question, and the guard's decision logic stays build-tag-free so
// it remains testable everywhere.
func systemFocusProbe() focusResult {
	h, ok := win32.ForegroundWindow()
	return focusResult{Handle: h, Known: ok}
}

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
	// Pin the window this paste is aimed at BEFORE touching the clipboard.
	// The keystroke below goes to whatever is focused when it fires. Without
	// this guard a window switch during `delay` delivers the remote path into
	// whatever the user moved to — a password manager, a chat box, a browser
	// URL bar.
	guard, err := newFocusGuard(systemFocusProbe)
	if err != nil {
		return err
	}

	if err := windowsSetClipboardText(remotePath); err != nil {
		return err
	}

	if delay > 0 {
		time.Sleep(delay)
	}

	if err := guard.verify(); err != nil {
		// Withhold the keystroke. cc-clip never snapshots the user's prior
		// clipboard, so the most we can undo is our own text write — put the
		// image back when the caller asked for restoration.
		if restoreClipboard {
			if restoreErr := windowsSetClipboardImage(imagePath); restoreErr != nil {
				return fmt.Errorf("%w (clipboard restore also failed: %v)", err, restoreErr)
			}
		}
		return err
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
$hr = [CcClipOle]::OleFlushClipboard()
if ($hr -ne 0) { throw "OleFlushClipboard failed with HRESULT 0x$($hr.ToString('X8'))" }`
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
  $hr = [CcClipOle]::OleFlushClipboard()
  if ($hr -ne 0) { throw "OleFlushClipboard failed with HRESULT 0x$($hr.ToString('X8'))" }
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

// windowsSendCtrlShiftV delivers the paste keystroke via SendInput.
//
// It used to shell out to WinForms SendKeys, which Electron/Chromium terminals
// (Wave, Hyper, Tabby, VS Code's integrated terminal) ignore outright while
// SendWait still returned success — so a paste that never happened was
// reported as a success by the log, the tray balloon and the exit code
// (issue #140). win32.SendCtrlShiftV checks how many events the input stream
// actually accepted, so a refusal surfaces as an error.
func windowsSendCtrlShiftV() error {
	if err := win32.SendCtrlShiftV(); err != nil {
		return fmt.Errorf("failed to send Ctrl+Shift+V: %w", err)
	}
	return nil
}
