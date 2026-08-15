package main

import (
	"os"
	"strings"
	"testing"
)

func TestResolveRemoteDir(t *testing.T) {
	t.Parallel()
	const home = "/home/u"
	tests := []struct {
		remoteDir string
		want      string
	}{
		{"~", home},
		{"~/.cache/cc-clip/uploads", "/home/u/.cache/cc-clip/uploads"},
		{"/var/tmp/up", "/var/tmp/up"},
		{"/var//tmp/../tmp/up", "/var/tmp/up"},
		{"uploads", "/home/u/uploads"},
	}
	for _, tt := range tests {
		if got := resolveRemoteDir(home, tt.remoteDir); got != tt.want {
			t.Errorf("resolveRemoteDir(%q,%q) = %q, want %q", home, tt.remoteDir, got, tt.want)
		}
	}
}

func TestImageExt(t *testing.T) {
	t.Parallel()
	tests := []struct{ format, want string }{
		{"jpeg", "jpg"},
		{"JPG", "jpg"},
		{" jpeg ", "jpg"},
		{"png", "png"},
		{"tiff", "png"}, // anything non-jpeg serves as png
		{"", "png"},
	}
	for _, tt := range tests {
		if got := imageExt(tt.format); got != tt.want {
			t.Errorf("imageExt(%q) = %q, want %q", tt.format, got, tt.want)
		}
	}
}

func TestRandomFilename(t *testing.T) {
	t.Parallel()
	a, err := randomFilename("png")
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomFilename("png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, "clip-") || !strings.HasSuffix(a, ".png") {
		t.Fatalf("filename shape: %q", a)
	}
	// The 4 random bytes exist precisely so two pastes in the same second
	// cannot collide on the remote.
	if a == b {
		t.Fatalf("two filenames must differ: %q", a)
	}
}

func TestWriteTempImage(t *testing.T) {
	t.Parallel()
	data := []byte{0x89, 0x50, 0x4E, 0x47}
	path, err := writeTempImage(data, "png")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("temp image must carry the extension: %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch: %v", got)
	}
}

func TestDescribeFileMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode os.FileMode
		want string
	}{
		{os.ModeDir, "directory"},
		{os.ModeSymlink, "symlink"},
		{os.ModeNamedPipe, "named pipe (FIFO)"},
		{os.ModeSocket, "socket"},
		{os.ModeDevice, "device"},
		{os.ModeCharDevice, "device"},
		{os.ModeIrregular, "irregular file"},
		{0, "non-regular file"},
	}
	for _, tt := range tests {
		if got := describeFileMode(tt.mode); got != tt.want {
			t.Errorf("describeFileMode(%v) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}
