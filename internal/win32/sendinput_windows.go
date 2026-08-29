//go:build windows

package win32

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// Virtual-key codes and SendInput flags we need. Values from WinUser.h.
const (
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // ALT
	vkLWin    = 0x5B
	vkRWin    = 0x5C
	vkV       = 0x56

	inputKeyboard  = 1
	keyeventfKeyUp = 0x0002
)

var (
	procSendInput        = user32DLL.NewProc("SendInput")
	procGetAsyncKeyState = user32DLL.NewProc("GetAsyncKeyState")
)

// keyboardInput mirrors KEYBDINPUT.
type keyboardInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// input mirrors INPUT with the keyboard arm of its union selected. The
// trailing padding stands in for the rest of the union: MOUSEINPUT is the
// largest member, so INPUT is 40 bytes on 64-bit Windows while KEYBDINPUT
// alone accounts for only 24 of them. SendInput validates cbSize against the
// real struct, so getting this wrong makes every call fail with
// ERROR_INVALID_PARAMETER rather than misbehaving subtly.
type input struct {
	inputType uint32
	_         [4]byte // union alignment: the union starts at offset 8
	ki        keyboardInput
	_         [8]byte // remainder of the union (MOUSEINPUT is 32 bytes)
}

// Compile-time guard on the layout above. Both constants must be
// non-negative, which is only true when INPUT is exactly 40 bytes. cc-clip
// ships windows/amd64 and windows/arm64; a 32-bit target would need a
// different layout and should fail loudly here rather than at runtime.
const (
	_ = uint(unsafe.Sizeof(input{})) - 40
	_ = 40 - uint(unsafe.Sizeof(input{}))
)

// keyDown returns true when the given virtual key is physically held right now.
func keyDown(vk uintptr) bool {
	r, _, _ := procGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

// SendCtrlShiftV synthesizes a Ctrl+Shift+V keystroke into the foreground
// window using SendInput.
//
// SendInput rather than SendKeys (WinForms) because Electron/Chromium windows
// — Wave, Hyper, Tabby, VS Code's integrated terminal — ignore SendKeys
// entirely while SendWait still returns success, so a dropped paste was
// indistinguishable from a delivered one in the log, the tray balloon and the
// exit code (issue #140). SendInput reports how many events it actually
// inserted, so a refusal becomes a real error.
//
// It also runs in-process. The old path spawned a PowerShell child, and that
// cold start sat inside the window between the focus guard's check and the
// keystroke actually landing.
func SendCtrlShiftV() error {
	// The caller usually arrives here straight from a modifier-bearing global
	// hotkey (alt+shift+v by default), and the user may still be holding those
	// keys. A physically-held ALT or WIN merges into the synthetic chord and
	// turns Ctrl+Shift+V into Ctrl+Alt+Shift+V or a Win chord, which the
	// terminal does not treat as paste. Release the ones that would corrupt
	// the chord; SHIFT and CTRL are part of what we are sending anyway.
	for _, vk := range []uintptr{vkMenu, vkLWin, vkRWin} {
		if keyDown(vk) {
			if err := sendInputs([]input{keyEvent(uint16(vk), true)}); err != nil {
				return fmt.Errorf("releasing held modifier 0x%02X: %w", vk, err)
			}
		}
	}
	// Let the release settle before the chord: the target window processes
	// these asynchronously, and a same-tick down/up pair can be observed out
	// of order.
	time.Sleep(15 * time.Millisecond)

	events := []input{
		keyEvent(vkControl, false),
		keyEvent(vkShift, false),
		keyEvent(vkV, false),
		keyEvent(vkV, true),
		keyEvent(vkShift, true),
		keyEvent(vkControl, true),
	}
	return sendInputs(events)
}

func keyEvent(vk uint16, up bool) input {
	in := input{inputType: inputKeyboard}
	in.ki.wVk = vk
	if up {
		in.ki.dwFlags = keyeventfKeyUp
	}
	return in
}

// sendInputs submits a batch and verifies every event was accepted.
//
// A short return means the input stream rejected the batch — most often UIPI:
// a process at a lower integrity level cannot inject into an elevated window,
// so pasting into a terminal running as administrator fails here. That used to
// be reported as success.
func sendInputs(events []input) error {
	if len(events) == 0 {
		return nil
	}
	sent, _, callErr := procSendInput.Call(
		uintptr(len(events)),
		uintptr(unsafe.Pointer(&events[0])),
		unsafe.Sizeof(events[0]),
	)
	if int(sent) != len(events) {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return fmt.Errorf("SendInput inserted %d of %d events: %w (a window running as administrator blocks input from a non-elevated process)", int(sent), len(events), errno)
		}
		return fmt.Errorf("SendInput inserted %d of %d events (a window running as administrator blocks input from a non-elevated process)", int(sent), len(events))
	}
	return nil
}
