//go:build !windows

package win32

import "os/exec"

// HideConsoleWindow is a no-op on non-Windows platforms.
func HideConsoleWindow(_ *exec.Cmd) {}
