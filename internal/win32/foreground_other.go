//go:build !windows

package win32

// ForegroundWindow has no meaning off Windows, so it always reports that the
// focused window is unknown. Callers fail closed on that, which is correct:
// the paste path it guards is Windows-only.
func ForegroundWindow() (handle uintptr, ok bool) {
	return 0, false
}
