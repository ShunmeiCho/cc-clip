//go:build windows

package doctor

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow prevents a console window flash when running child processes.
func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
