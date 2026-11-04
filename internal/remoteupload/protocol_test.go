package remoteupload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func frame(t *testing.T, data []byte) (Header, []byte) {
	t.Helper()
	hash := sha256.Sum256(data)
	h := Header{ProtocolVersion, int64(len(data)), hex.EncodeToString(hash[:]), "png"}
	meta, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return h, append(append(meta, '\n'), data...)
}

func TestReceiveBinaryAndUniqueFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "图片 with spaces")
	data := bytes.Repeat([]byte{0, 255, 13, 10, 26, 128}, 70000)
	h, input := frame(t, data)
	a, err := Receive(bytes.NewReader(input), root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Receive(bytes.NewReader(input), root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path || a.Size != h.Size || a.SHA256 != h.SHA256 || a.Protocol != ProtocolVersion {
		t.Fatalf("invalid receipt: %+v %+v", a, b)
	}
	got, err := os.ReadFile(a.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("binary image changed: %v", err)
	}
	if filepath.Dir(a.Path) != root || filepath.Ext(a.Path) != ".png" {
		t.Fatalf("unexpected upload path: %s", a.Path)
	}
}

func TestReceiveRejectsBrokenFramesAndCleansFiles(t *testing.T) {
	_, valid := frame(t, []byte{0, 1, 2, 255})
	for name, input := range map[string][]byte{
		"truncated":        valid[:len(valid)-1],
		"trailing":         append(append([]byte{}, valid...), 9),
		"corrupt":          append(append([]byte{}, valid[:len(valid)-1]...), 0),
		"invalid json":     []byte("{oops}\nabc"),
		"oversized header": []byte(strings.Repeat("a", maxHeaderBytes) + "\nabc"),
		"missing newline":  []byte("{}"),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Receive(bytes.NewReader(input), root); err == nil {
				t.Fatal("broken frame accepted")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed upload left files: %v %v", entries, err)
			}
		})
	}
}

func TestHeaderRejectsUnboundedDataAndFileNames(t *testing.T) {
	valid, _ := frame(t, []byte{1})
	for _, change := range []func(*Header){
		func(h *Header) { h.Size = 0 }, func(h *Header) { h.Size = MaxImageBytes + 1 },
		func(h *Header) { h.Size = -1 }, func(h *Header) { h.Protocol++ },
		func(h *Header) { h.SHA256 = "bad" }, func(h *Header) { h.Extension = "../exe" },
		func(h *Header) { h.Extension = "exe" }, func(h *Header) { h.Extension = "png:stream" },
	} {
		h := valid
		change(&h)
		if err := h.Validate(); err == nil {
			t.Fatalf("accepted invalid header: %+v", h)
		}
	}
}
