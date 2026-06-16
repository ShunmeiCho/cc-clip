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
)

const (
	biRGB       = 0
	biBitFields = 3

	maxClipboardDIBSize = 80 * 1024 * 1024
)

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, clipboardOutputLimitError{msg: fmt.Sprintf("encoded image exceeds %dMB limit", maxImageMB())}
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
	maxPixels := int64(maxClipboardDIBSize / 4)
	if int64(width) > maxPixels || int64(height) > maxPixels || int64(width)*int64(height) > maxPixels {
		return nil, clipboardOutputLimitError{msg: "DIB dimensions exceed limit"}
	}

	planes := binary.LittleEndian.Uint16(dib[12:14])
	bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])
	if planes != 1 {
		return nil, fmt.Errorf("unsupported DIB planes %d", planes)
	}
	if compression != biRGB && compression != biBitFields {
		return nil, fmt.Errorf("unsupported DIB compression %d", compression)
	}
	if bpp != 24 && bpp != 32 {
		return nil, fmt.Errorf("unsupported DIB bit depth %d", bpp)
	}

	masks, masksBytes, err := dibMasks(dib, headerSize, bpp, compression)
	if err != nil {
		return nil, err
	}

	pixelOffset := headerSize + masksBytes
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
