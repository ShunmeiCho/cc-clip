//go:build windows

package win32

import "syscall"

var (
	user32DLL             = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWind = user32DLL.NewProc("GetForegroundWindow")
)

// ForegroundWindow returns a handle to the window the user is currently
// working in, and whether it could be determined.
//
// GetForegroundWindow returns NULL when no window has focus — notably while a
// window is losing activation. That is reported as ok=false rather than as
// handle 0 so callers cannot accidentally compare two "no window" observations
// and conclude focus was unchanged.
func ForegroundWindow() (handle uintptr, ok bool) {
	h, _, _ := procGetForegroundWind.Call()
	if h == 0 {
		return 0, false
	}
	return h, true
}
