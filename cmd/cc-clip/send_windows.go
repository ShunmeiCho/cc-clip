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

func windowsSetClipboardText(text string) error {
	script := `Set-Clipboard -Value $env:CC_CLIP_TEXT`
	cmd := hiddenExec("powershell", "-STA", "-NoProfile", "-Command", script)
	cmd.Env = append(os.Environ(), "CC_CLIP_TEXT="+text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set text clipboard: %s: %w", string(out), err)
	}
	return nil
}

func windowsSetClipboardImage(imagePath string) error {
	script := `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Drawing.Image]::FromFile($env:CC_CLIP_IMAGE_PATH)
try {
  [System.Windows.Forms.Clipboard]::SetImage($img)
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
