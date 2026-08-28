//go:build !windows

package win32

import "errors"

// SendCtrlShiftV has no meaning off Windows. The synthetic-paste path it
// serves is Windows-only, so callers never reach this on other platforms.
func SendCtrlShiftV() error {
	return errors.New("synthetic keystrokes are only supported on Windows")
}
