//go:build windows

package daemon

import (
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const cfHDROP = 15

// DroppedImageFile returns the first image file path referenced by a CF_HDROP
// clipboard payload. Explorer-style "copy" of an image file (or its thumbnail)
// puts only a file-path list on the clipboard — no bitmap — so this lets such a
// copy be treated as an upload source. Returns "" when the clipboard holds no
// dropped image file.
func DroppedImageFile() string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := openClipboard(); err != nil {
		return ""
	}
	defer closeClipboard()

	if !clipboardFormatAvailable(cfHDROP) {
		return ""
	}
	h, _, _ := procGetClipboardData.Call(uintptr(cfHDROP))
	if h == 0 {
		return ""
	}
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return ""
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)

	// The block starts with a DROPFILES header whose pFiles field is the byte
	// offset to the first path; the paths then form a UTF-16 double-NUL-
	// terminated array (pFiles is normally 20, i.e. sizeof(DROPFILES)).
	u16 := make([]uint16, int(size)/2)
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&u16[0])), ptr, size)
	if len(u16) < 10 {
		return ""
	}
	start := int(uint32(u16[0])|uint32(u16[1])<<16) / 2
	for i := start; i < len(u16); {
		j := i
		for j < len(u16) && u16[j] != 0 {
			j++
		}
		if j == i {
			return ""
		}
		p := syscall.UTF16ToString(u16[i:j])
		if isImageFilePath(p) {
			return p
		}
		i = j + 1
	}
	return ""
}

func isImageFilePath(p string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(p), ".")) {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic":
		return true
	}
	return false
}
