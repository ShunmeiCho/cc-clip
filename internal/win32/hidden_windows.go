//go:build windows

package win32

import (
	"os/exec"
	"syscall"
)

// HideConsoleWindow prevents a console window flash when running child processes.
func HideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
