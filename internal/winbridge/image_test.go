package winbridge

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestFormatsPreserveColorAlphaAndBottomUpOrder(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	img.SetNRGBA(1, 1, color.NRGBA{B: 255, A: 255})
	var input bytes.Buffer
	if err := png.Encode(&input, img); err != nil {
		t.Fatal(err)
	}
	encoded, dib, err := Formats(input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(dib) != 124+16 || binary.LittleEndian.Uint32(dib[:4]) != 124 || binary.LittleEndian.Uint32(dib[52:56]) != 0xff000000 {
		t.Fatal("invalid DIBV5 header")
	}
	if !bytes.Equal(dib[128:132], []byte{255, 0, 0, 255}) || !bytes.Equal(dib[132:136], []byte{50, 100, 200, 128}) {
		t.Fatalf("DIBV5 pixels were flipped/swizzled incorrectly: %v", dib[124:])
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA); got != img.NRGBAAt(0, 0) {
		t.Fatalf("PNG lost alpha/color: %+v", got)
	}
}

func TestFormatsRejectCompressedImageBombBeforeDecode(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	var input bytes.Buffer
	png.Encode(&input, img)
	data := append([]byte(nil), input.Bytes()...)
	binary.BigEndian.PutUint32(data[16:20], 100000)
	binary.BigEndian.PutUint32(data[20:24], 100000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, _, err := Formats(data); err == nil {
		t.Fatal("accepted unbounded decoded image")
	}
	for _, data := range [][]byte{nil, []byte("not an image"), make([]byte, MaxEncodedBytes+1)} {
		if _, _, err := Formats(data); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
}
