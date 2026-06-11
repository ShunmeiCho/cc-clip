//go:build windows

package daemon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/bits"
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

	biRGB       = 0
	biBitFields = 3

	maxClipboardDIBSize = 80 * 1024 * 1024
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

	if data, ok, err := readClipboardGlobalBytes(registeredPNGFormat(), int64(maxImageSize())); ok || err != nil {
		if err == nil {
			c.storeCachedImage(seq, "png", data)
		}
		return data, err
	}
	if data, ok, err := readClipboardGlobalBytes(cfDIBV5, maxClipboardDIBSize); ok || err != nil {
		if err != nil {
			return nil, err
		}
		pngData, err := pngFromDIB(data)
		if err == nil {
			c.storeCachedImage(seq, "png", pngData)
		}
		return pngData, err
	}
	if data, ok, err := readClipboardGlobalBytes(cfDIB, maxClipboardDIBSize); ok || err != nil {
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

func readClipboardGlobalBytes(format uint32, maxBytes int64) ([]byte, bool, error) {
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
		return nil, true, fmt.Errorf("clipboard image exceeds limit")
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

func readClipboardUnicodeText() (string, bool, error) {
	data, ok, err := readClipboardGlobalBytes(cfUnicodeText, int64(maxTextSize())*2+2)
	if !ok || err != nil {
		return "", ok, err
	}
	text, err := decodeUnicodeClipboardText(data)
	return text, true, err
}

func decodeUnicodeClipboardText(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("clipboard Unicode text has odd byte length")
	}
	u16 := make([]uint16, len(data)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(data[i*2 : i*2+2])
	}
	text := syscall.UTF16ToString(u16)
	if text == "" {
		return "", fmt.Errorf("clipboard text is empty")
	}
	if len(text) > maxTextSize() {
		return "", fmt.Errorf("clipboard text exceeds %dMB limit", maxTextMB())
	}
	return text, nil
}

func readClipboardANSIText() (string, bool, error) {
	data, ok, err := readClipboardGlobalBytes(cfText, int64(maxTextSize())+1)
	if !ok || err != nil {
		return "", ok, err
	}
	text, err := decodeANSIClipboardText(data)
	return text, true, err
}

func decodeANSIClipboardText(data []byte) (string, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		data = data[:i]
	}
	if len(data) == 0 {
		return "", fmt.Errorf("clipboard text is empty")
	}
	if len(data) > maxTextSize() {
		return "", fmt.Errorf("clipboard text exceeds %dMB limit", maxTextMB())
	}
	return string(data), nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("encoded image exceeds %dMB limit", maxImageMB())
	}
	return b.Buffer.Write(p)
}

func pngFromDIB(dib []byte) ([]byte, error) {
	img, err := imageFromDIB(dib)
	if err != nil {
		return nil, err
	}
	var out cappedBuffer
	out.limit = maxImageSize()
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("encoded PNG is empty")
	}
	return out.Bytes(), nil
}

func imageFromDIB(dib []byte) (image.Image, error) {
	if len(dib) < 40 {
		return nil, fmt.Errorf("DIB too small")
	}
	headerSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	if headerSize < 40 || headerSize > len(dib) {
		return nil, fmt.Errorf("invalid DIB header size %d", headerSize)
	}
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	rawHeight := int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	if width <= 0 || rawHeight == 0 {
		return nil, fmt.Errorf("invalid DIB dimensions %dx%d", width, rawHeight)
	}
	height := rawHeight
	topDown := false
	if height < 0 {
		topDown = true
		height = -height
	}
	if width > maxClipboardDIBSize/4 || height > maxClipboardDIBSize/4 || width*height > maxClipboardDIBSize/4 {
		return nil, fmt.Errorf("DIB dimensions exceed limit")
	}

	planes := binary.LittleEndian.Uint16(dib[12:14])
	bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])
	clrUsed := uint32(0)
	if len(dib) >= 36 {
		clrUsed = binary.LittleEndian.Uint32(dib[32:36])
	}
	if planes != 1 {
		return nil, fmt.Errorf("unsupported DIB planes %d", planes)
	}
	if compression != biRGB && compression != biBitFields {
		return nil, fmt.Errorf("unsupported DIB compression %d", compression)
	}

	masks, masksBytes, err := dibMasks(dib, headerSize, bpp, compression)
	if err != nil {
		return nil, err
	}

	pixelOffset := headerSize + masksBytes
	if bpp <= 8 {
		colors := int(clrUsed)
		if colors == 0 {
			colors = 1 << bpp
		}
		pixelOffset += colors * 4
	}
	if pixelOffset < 0 || pixelOffset > len(dib) {
		return nil, fmt.Errorf("invalid DIB pixel offset")
	}

	stride := dibStride(width, bpp)
	if stride <= 0 || stride > len(dib) {
		return nil, fmt.Errorf("invalid DIB stride")
	}
	if len(dib)-pixelOffset < stride*height {
		return nil, fmt.Errorf("DIB pixel data truncated")
	}

	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	switch bpp {
	case 24:
		if compression != biRGB {
			return nil, fmt.Errorf("unsupported 24-bit DIB compression %d", compression)
		}
		fill24BitDIB(dst, dib[pixelOffset:], stride, topDown)
	case 32:
		fill32BitDIB(dst, dib[pixelOffset:], stride, topDown, masks)
	default:
		return nil, fmt.Errorf("unsupported DIB bit depth %d", bpp)
	}
	return dst, nil
}

func dibStride(width, bpp int) int {
	if width <= 0 || bpp <= 0 {
		return 0
	}
	bitsPerRow := width * bpp
	return ((bitsPerRow + 31) / 32) * 4
}

type colorMasks struct {
	r uint32
	g uint32
	b uint32
	a uint32
}

func dibMasks(dib []byte, headerSize, bpp int, compression uint32) (colorMasks, int, error) {
	if bpp != 32 {
		return colorMasks{}, 0, nil
	}
	if compression == biRGB {
		return colorMasks{r: 0x00ff0000, g: 0x0000ff00, b: 0x000000ff, a: 0xff000000}, 0, nil
	}
	if headerSize >= 56 {
		return colorMasks{
			r: binary.LittleEndian.Uint32(dib[40:44]),
			g: binary.LittleEndian.Uint32(dib[44:48]),
			b: binary.LittleEndian.Uint32(dib[48:52]),
			a: binary.LittleEndian.Uint32(dib[52:56]),
		}, 0, nil
	}
	if headerSize == 40 {
		if len(dib) < headerSize+12 {
			return colorMasks{}, 0, fmt.Errorf("DIB bitfield masks truncated")
		}
		return colorMasks{
			r: binary.LittleEndian.Uint32(dib[40:44]),
			g: binary.LittleEndian.Uint32(dib[44:48]),
			b: binary.LittleEndian.Uint32(dib[48:52]),
			a: 0,
		}, 12, nil
	}
	return colorMasks{}, 0, fmt.Errorf("unsupported DIB bitfield header size %d", headerSize)
}

func fill24BitDIB(dst *image.NRGBA, pixels []byte, stride int, topDown bool) {
	bounds := dst.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		srcY := y
		if !topDown {
			srcY = bounds.Dy() - 1 - y
		}
		row := pixels[srcY*stride:]
		for x := 0; x < bounds.Dx(); x++ {
			i := x * 3
			dst.SetNRGBA(x, y, color.NRGBA{R: row[i+2], G: row[i+1], B: row[i], A: 255})
		}
	}
}

func fill32BitDIB(dst *image.NRGBA, pixels []byte, stride int, topDown bool, masks colorMasks) {
	bounds := dst.Bounds()
	anyAlpha := false
	for y := 0; y < bounds.Dy(); y++ {
		srcY := y
		if !topDown {
			srcY = bounds.Dy() - 1 - y
		}
		row := pixels[srcY*stride:]
		for x := 0; x < bounds.Dx(); x++ {
			v := binary.LittleEndian.Uint32(row[x*4 : x*4+4])
			a := byte(255)
			if masks.a != 0 {
				a = scaleMask(v, masks.a)
				if a != 0 {
					anyAlpha = true
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: scaleMask(v, masks.r),
				G: scaleMask(v, masks.g),
				B: scaleMask(v, masks.b),
				A: a,
			})
		}
	}
	if masks.a != 0 && !anyAlpha {
		for i := 3; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = 255
		}
	}
}

func scaleMask(v, mask uint32) byte {
	if mask == 0 {
		return 0
	}
	shift := bits.TrailingZeros32(mask)
	n := bits.OnesCount32(mask)
	raw := (v & mask) >> shift
	if n >= 8 {
		return byte(raw >> (n - 8))
	}
	return byte((raw * 255) / ((1 << n) - 1))
}
