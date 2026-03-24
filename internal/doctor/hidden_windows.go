//go:build windows

package doctor

import (
	"os/exec"

	"github.com/shunmei/cc-clip/internal/win32"
)

func hideConsoleWindow(cmd *exec.Cmd) {
	win32.HideConsoleWindow(cmd)
}
