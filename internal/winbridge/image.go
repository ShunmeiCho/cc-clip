package winbridge

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
)

const MaxEncodedBytes = 20 << 20
const MaxPixelBytes = 80 << 20

// Formats returns a canonical PNG and a bottom-up, unpremultiplied BGRA
// BITMAPV5HEADER suitable for native clipboard consumers such as arboard.
func Formats(data []byte) ([]byte, []byte, error) {
	if len(data) == 0 || len(data) > MaxEncodedBytes {
		return nil, nil, fmt.Errorf("clipboard image exceeds encoded size limit or is empty")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixelBytes/4 {
		return nil, nil, fmt.Errorf("clipboard image dimensions exceed decoded size limit")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	w, h := cfg.Width, cfg.Height
	dib := make([]byte, 124+w*h*4)
	put := func(at int, value uint32) { binary.LittleEndian.PutUint32(dib[at:at+4], value) }
	put(0, 124)
	put(4, uint32(w))
	put(8, uint32(h))
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	put(16, 3)
	put(20, uint32(w*h*4)) // BI_BITFIELDS
	put(40, 0xff0000)
	put(44, 0xff00)
	put(48, 0xff)
	put(52, 0xff000000)
	put(56, 0x73524742)
	put(108, 4) // sRGB, LCS_GM_IMAGES
	bounds := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			i := 124 + ((h-1-y)*w+x)*4
			dib[i], dib[i+1], dib[i+2], dib[i+3] = p.B, p.G, p.R, p.A
		}
	}
	var encoded limitedBuffer
	if err := png.Encode(&encoded, img); err != nil {
		return nil, nil, err
	}
	return encoded.Bytes(), dib, nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxEncodedBytes {
		return 0, fmt.Errorf("canonical PNG exceeds size limit")
	}
	return b.Buffer.Write(p)
}
