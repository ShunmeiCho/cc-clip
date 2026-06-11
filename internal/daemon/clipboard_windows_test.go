//go:build windows

package daemon

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"
	"unicode/utf16"
)

func TestImageFromDIB24BitBottomUp(t *testing.T) {
	dib := make([]byte, 40+16)
	writeDIBHeader(dib, 2, 2, 24, biRGB)

	// Bottom-up DIB memory: bottom row first, BGR pixels, rows DWORD padded.
	pixels := dib[40:]
	copy(pixels[0:8], []byte{
		255, 0, 0, // bottom-left blue
		255, 255, 255, // bottom-right white
		0, 0, // padding
	})
	copy(pixels[8:16], []byte{
		0, 0, 255, // top-left red
		0, 255, 0, // top-right green
		0, 0, // padding
	})

	img, err := imageFromDIB(dib)
	if err != nil {
		t.Fatalf("imageFromDIB returned error: %v", err)
	}

	assertPixel(t, img.At(0, 0), color.NRGBA{R: 255, A: 255})
	assertPixel(t, img.At(1, 0), color.NRGBA{G: 255, A: 255})
	assertPixel(t, img.At(0, 1), color.NRGBA{B: 255, A: 255})
	assertPixel(t, img.At(1, 1), color.NRGBA{R: 255, G: 255, B: 255, A: 255})
}

func TestImageFromDIB32BitAlphaZeroBecomesOpaque(t *testing.T) {
	dib := make([]byte, 40+4)
	writeDIBHeader(dib, 1, -1, 32, biRGB)
	copy(dib[40:], []byte{30, 20, 10, 0}) // BGRA, alpha byte often unused for BI_RGB.

	img, err := imageFromDIB(dib)
	if err != nil {
		t.Fatalf("imageFromDIB returned error: %v", err)
	}
	assertPixel(t, img.At(0, 0), color.NRGBA{R: 10, G: 20, B: 30, A: 255})
}

func TestPNGFromDIBEncodesPNG(t *testing.T) {
	dib := make([]byte, 40+4)
	writeDIBHeader(dib, 1, 1, 24, biRGB)
	copy(dib[40:], []byte{3, 2, 1, 0})

	out, err := pngFromDIB(dib)
	if err != nil {
		t.Fatalf("pngFromDIB returned error: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("encoded output is not PNG: %v", err)
	}
}

func TestWindowsClipboardCacheReturnsCopies(t *testing.T) {
	c := &windowsClipboard{}
	original := []byte{1, 2, 3}
	c.storeCachedImage(42, "png", original)
	original[0] = 9

	got, ok := c.cachedImage(42)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got[0] != 1 {
		t.Fatalf("cache kept caller-owned backing array, got first byte %d", got[0])
	}

	got[1] = 9
	again, ok := c.cachedImage(42)
	if !ok {
		t.Fatal("expected second cache hit")
	}
	if again[1] != 2 {
		t.Fatalf("cache returned mutable backing array, got second byte %d", again[1])
	}
}

func TestWindowsClipboardCacheIgnoresZeroSequence(t *testing.T) {
	c := &windowsClipboard{}
	c.storeCachedImage(0, "png", []byte{1, 2, 3})
	if _, ok := c.cachedImage(0); ok {
		t.Fatal("sequence 0 must not be cached")
	}
}

func TestWindowsClipboardCacheType(t *testing.T) {
	c := &windowsClipboard{}
	if _, ok := c.cachedType(7); ok {
		t.Fatal("empty cache should miss")
	}
	c.storeCachedImage(7, "png", []byte{1})
	got, ok := c.cachedType(7)
	if !ok {
		t.Fatal("expected cached type hit")
	}
	if got.Type != ClipboardImage || got.Format != "png" {
		t.Fatalf("cached type = %#v, want image/png", got)
	}
	c.storeCachedText(8, "hello")
	got, ok = c.cachedType(8)
	if !ok {
		t.Fatal("expected cached text type hit")
	}
	if got.Type != ClipboardText || got.Format != "" {
		t.Fatalf("cached type = %#v, want text", got)
	}
}

func TestWindowsClipboardTextCache(t *testing.T) {
	c := &windowsClipboard{}
	c.storeCachedText(42, "hello")

	got, ok := c.cachedText(42)
	if !ok {
		t.Fatal("expected text cache hit")
	}
	if got != "hello" {
		t.Fatalf("cached text = %q, want hello", got)
	}
}

func TestDecodeUnicodeClipboardTextStopsAtNUL(t *testing.T) {
	u16 := utf16.Encode([]rune("hello"))
	u16 = append(u16, 0, 'x')
	data := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(data[i*2:i*2+2], v)
	}

	got, err := decodeUnicodeClipboardText(data)
	if err != nil {
		t.Fatalf("decodeUnicodeClipboardText returned error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("decoded text = %q, want hello", got)
	}
}

func TestDecodeANSIClipboardTextStopsAtNUL(t *testing.T) {
	got, err := decodeANSIClipboardText([]byte("hello\x00ignored"))
	if err != nil {
		t.Fatalf("decodeANSIClipboardText returned error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("decoded text = %q, want hello", got)
	}
}

func writeDIBHeader(dib []byte, width, height int32, bpp uint16, compression uint32) {
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(width))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(height))
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], bpp)
	binary.LittleEndian.PutUint32(dib[16:20], compression)
}

func assertPixel(t *testing.T, got color.Color, want color.NRGBA) {
	t.Helper()
	c := color.NRGBAModel.Convert(got).(color.NRGBA)
	if c != want {
		t.Fatalf("pixel = %#v, want %#v", c, want)
	}
}
