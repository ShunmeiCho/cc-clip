//go:build windows

package winbridge

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// ReadFormat is a diagnostic native clipboard consumer; it makes no writes.
func ReadFormat(format string) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	id := uintptr(cfDIBV5)
	if format == "png" {
		id, _, _ = registerFormat.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("PNG"))))
	} else if format != "dibv5" {
		return nil, fmt.Errorf("unknown clipboard format")
	}
	if r, _, _ := user.NewProc("IsClipboardFormatAvailable").Call(id); r == 0 {
		return nil, fmt.Errorf("native clipboard image format %s is unavailable", format)
	}
	opened := false
	for i := 0; i < 10; i++ {
		if r, _, _ := openClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !opened {
		return nil, fmt.Errorf("native clipboard is busy")
	}
	defer closeClipboard.Call()
	h, _, err := user.NewProc("GetClipboardData").Call(id)
	if h == 0 {
		return nil, fmt.Errorf("GetClipboardData(%s): %v", format, err)
	}
	size, _, _ := kernel.NewProc("GlobalSize").Call(h)
	if size == 0 || size > MaxPixelBytes+124 {
		return nil, fmt.Errorf("native clipboard memory exceeds size limit")
	}
	p, _, err := globalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("GlobalLock: %v", err)
	}
	defer globalUnlock.Call(h)
	data := make([]byte, int(size))
	copyMemory.Call(uintptr(unsafe.Pointer(&data[0])), p, size)
	runtime.KeepAlive(data)
	return data, nil
}
