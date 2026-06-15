//go:build windows

package daemon

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	cfText        = 1
	cfDIB         = 8
	cfUnicodeText = 13
	cfDIBV5       = 17
)

var (
	user32DLL                      = syscall.NewLazyDLL("user32.dll")
	kernelDLL                      = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard              = user32DLL.NewProc("OpenClipboard")
	procCloseClipboard             = user32DLL.NewProc("CloseClipboard")
	procIsClipboardFormatAvailable = user32DLL.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData           = user32DLL.NewProc("GetClipboardData")
	procGetClipboardSequenceNumber = user32DLL.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW   = user32DLL.NewProc("RegisterClipboardFormatW")
	procGlobalLock                 = kernelDLL.NewProc("GlobalLock")
	procGlobalUnlock               = kernelDLL.NewProc("GlobalUnlock")
	procGlobalSize                 = kernelDLL.NewProc("GlobalSize")
	procRtlMoveMemory              = kernelDLL.NewProc("RtlMoveMemory")
)

type windowsClipboard struct {
	mu    sync.Mutex
	cache windowsClipboardCache
}

type windowsClipboardCache struct {
	seq    uint32
	kind   ClipboardType
	format string
	data   []byte
	text   string
}

func NewClipboardReader() ClipboardReader {
	return &windowsClipboard{}
}

func (c *windowsClipboard) Type() (ClipboardInfo, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if info, ok := c.cachedType(clipboardSequenceNumber()); ok {
		return info, nil
	}

	if err := openClipboard(); err != nil {
		return ClipboardInfo{}, err
	}
	defer closeClipboard()

	switch {
	case clipboardFormatAvailable(registeredPNGFormat()):
		return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
	case clipboardFormatAvailable(cfDIBV5), clipboardFormatAvailable(cfDIB):
		return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
	case clipboardFormatAvailable(cfUnicodeText):
		return ClipboardInfo{Type: ClipboardText}, nil
	case clipboardFormatAvailable(cfText):
		return ClipboardInfo{Type: ClipboardText}, nil
	default:
		return ClipboardInfo{Type: ClipboardEmpty}, nil
	}
}

func (c *windowsClipboard) ImageBytes() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	seq := clipboardSequenceNumber()
	if data, ok := c.cachedImage(seq); ok {
		return data, nil
	}

	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer closeClipboard()

	if data, ok, err := readClipboardGlobalBytes(registeredPNGFormat(), int64(maxImageSize()), fmt.Sprintf("clipboard image exceeds %dMB limit", maxImageMB())); ok || err != nil {
		if err == nil {
			c.storeCachedImage(seq, "png", data)
		}
		return data, err
	}
	if data, ok, err := readClipboardGlobalBytes(cfDIBV5, maxClipboardDIBSize, "clipboard DIB image exceeds 80MB limit"); ok || err != nil {
		if err != nil {
			return nil, err
		}
		pngData, err := pngFromDIB(data)
		if err == nil {
			c.storeCachedImage(seq, "png", pngData)
		}
		return pngData, err
	}
	if data, ok, err := readClipboardGlobalBytes(cfDIB, maxClipboardDIBSize, "clipboard DIB image exceeds 80MB limit"); ok || err != nil {
		if err != nil {
			return nil, err
		}
		pngData, err := pngFromDIB(data)
		if err == nil {
			c.storeCachedImage(seq, "png", pngData)
		}
		return pngData, err
	}
	return nil, fmt.Errorf("no image in clipboard")
}

func (c *windowsClipboard) Text() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	seq := clipboardSequenceNumber()
	if text, ok := c.cachedText(seq); ok {
		return text, nil
	}

	if err := openClipboard(); err != nil {
		return "", err
	}
	defer closeClipboard()

	if text, ok, err := readClipboardUnicodeText(); ok || err != nil {
		if err == nil {
			c.storeCachedText(seq, text)
		}
		return text, err
	}
	if text, ok, err := readClipboardANSIText(); ok || err != nil {
		if err == nil {
			c.storeCachedText(seq, text)
		}
		return text, err
	}
	return "", fmt.Errorf("no text in clipboard")
}

func (c *windowsClipboard) cachedImage(seq uint32) ([]byte, bool) {
	if seq == 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache.seq != seq || c.cache.kind != ClipboardImage || len(c.cache.data) == 0 {
		return nil, false
	}
	out := make([]byte, len(c.cache.data))
	copy(out, c.cache.data)
	return out, true
}

func (c *windowsClipboard) cachedText(seq uint32) (string, bool) {
	if seq == 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache.seq != seq || c.cache.kind != ClipboardText || c.cache.text == "" {
		return "", false
	}
	return c.cache.text, true
}

func (c *windowsClipboard) cachedType(seq uint32) (ClipboardInfo, bool) {
	if seq == 0 {
		return ClipboardInfo{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache.seq != seq {
		return ClipboardInfo{}, false
	}
	switch c.cache.kind {
	case ClipboardImage:
		if len(c.cache.data) == 0 {
			return ClipboardInfo{}, false
		}
		format := c.cache.format
		if format == "" {
			format = "png"
		}
		return ClipboardInfo{Type: ClipboardImage, Format: format}, true
	case ClipboardText:
		if c.cache.text == "" {
			return ClipboardInfo{}, false
		}
		return ClipboardInfo{Type: ClipboardText}, true
	default:
		return ClipboardInfo{}, false
	}
}

func (c *windowsClipboard) storeCachedImage(seq uint32, format string, data []byte) {
	if seq == 0 || len(data) == 0 || len(data) > maxImageSize() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = windowsClipboardCache{
		seq:    seq,
		kind:   ClipboardImage,
		format: format,
		data:   append([]byte(nil), data...),
	}
}

func (c *windowsClipboard) storeCachedText(seq uint32, text string) {
	if seq == 0 || text == "" || len(text) > maxTextSize() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = windowsClipboardCache{
		seq:  seq,
		kind: ClipboardText,
		text: text,
	}
}

func clipboardSequenceNumber() uint32 {
	r1, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(r1)
}

func openClipboard() error {
	r1, _, err := procOpenClipboard.Call(0)
	if r1 == 0 {
		return fmt.Errorf("OpenClipboard failed: %v", err)
	}
	return nil
}

func closeClipboard() {
	procCloseClipboard.Call()
}

func clipboardFormatAvailable(format uint32) bool {
	if format == 0 {
		return false
	}
	r1, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return r1 != 0
}

func registeredPNGFormat() uint32 {
	p, err := syscall.UTF16PtrFromString("PNG")
	if err != nil {
		return 0
	}
	r1, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(r1)
}

func readClipboardGlobalBytes(format uint32, maxBytes int64, tooLargeMsg string) ([]byte, bool, error) {
	if !clipboardFormatAvailable(format) {
		return nil, false, nil
	}
	h, _, err := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil, true, fmt.Errorf("GetClipboardData(%d) failed: %v", format, err)
	}
	size, _, err := procGlobalSize.Call(h)
	if size == 0 {
		return nil, true, fmt.Errorf("GlobalSize(%d) failed: %v", format, err)
	}
	if size > uintptr(maxBytes) {
		return nil, true, clipboardOutputLimitError{msg: tooLargeMsg}
	}
	ptr, _, err := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, true, fmt.Errorf("GlobalLock(%d) failed: %v", format, err)
	}
	defer procGlobalUnlock.Call(h)

	out := make([]byte, int(size))
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&out[0])), ptr, size)
	if len(out) == 0 {
		return nil, true, fmt.Errorf("clipboard image is empty")
	}
	return out, true, nil
}
