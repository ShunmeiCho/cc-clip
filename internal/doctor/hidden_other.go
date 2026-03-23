//go:build !windows

package doctor

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
