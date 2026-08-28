//go:build windows

package win32

import (
	"testing"
	"unsafe"
)

// TestInputStructLayout pins the INPUT layout SendInput validates via cbSize.
// A wrong size does not corrupt anything — SendInput simply rejects every call
// with ERROR_INVALID_PARAMETER, which would silently reinstate exactly the
// "paste never happens but nothing reports a failure" behaviour that replacing
// SendKeys was meant to end.
func TestInputStructLayout(t *testing.T) {
	if got := unsafe.Sizeof(input{}); got != 40 {
		t.Fatalf("sizeof(INPUT) = %d, want 40 (64-bit Windows layout)", got)
	}
	if got := unsafe.Offsetof(input{}.ki); got != 8 {
		t.Errorf("KEYBDINPUT starts at offset %d, want 8", got)
	}
	if got := unsafe.Sizeof(keyboardInput{}); got != 24 {
		t.Errorf("sizeof(KEYBDINPUT) = %d, want 24", got)
	}
}

func TestKeyEventFlags(t *testing.T) {
	down := keyEvent(vkV, false)
	if down.inputType != inputKeyboard {
		t.Errorf("inputType = %d, want %d", down.inputType, inputKeyboard)
	}
	if down.ki.wVk != vkV {
		t.Errorf("wVk = %#x, want %#x", down.ki.wVk, vkV)
	}
	if down.ki.dwFlags != 0 {
		t.Errorf("key-down dwFlags = %#x, want 0", down.ki.dwFlags)
	}

	up := keyEvent(vkV, true)
	if up.ki.dwFlags != keyeventfKeyUp {
		t.Errorf("key-up dwFlags = %#x, want %#x", up.ki.dwFlags, keyeventfKeyUp)
	}
}

// TestSendInputsEmptyIsNoop guards the guard: an empty batch must not index
// events[0].
func TestSendInputsEmptyIsNoop(t *testing.T) {
	if err := sendInputs(nil); err != nil {
		t.Fatalf("sendInputs(nil) = %v, want nil", err)
	}
}
