package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v0.6.1", "v0.6.1"},
		{"0.6.1", "v0.6.1"},
		{"  v0.6.1 ", "v0.6.1"},
		{"cc-clip 0.6.1", "v0.6.1"},
		{"v0.6.1-rc1", "v0.6.1-rc1"},
		{"v0.6.1+build.7", "v0.6.1+build.7"},
		{"dev", ""},
		{"", ""},
		{"abc123", ""},      // git-describe SHA-like
		{"v0.6", ""},        // only two components
		{"v0.x.1", ""},      // non-numeric
		{"v0.6.abc", ""},    // non-numeric patch with no suffix sep
	}
	for _, tc := range cases {
		got := normalizeVersion(tc.in)
		if got != tc.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseLsofPID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{
			name: "empty",
			raw:  "",
			want: 0,
		},
		{
			name: "single listener",
			raw: `p12345
n127.0.0.1:18339
`,
			want: 12345,
		},
		{
			name: "pid before n",
			raw:  "p92902\nn127.0.0.1:18339\n",
			want: 92902,
		},
		{
			name: "first pid wins if multiple",
			raw: `p111
n127.0.0.1:18339
p222
n127.0.0.1:18339
`,
			want: 111,
		},
		{
			name: "non-numeric pid",
			raw:  "pabc\n",
			want: -1, // sentinel; we expect an error
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLsofPID(tc.raw)
			if tc.want == -1 {
				if err == nil {
					t.Fatalf("expected error for malformed p-line, got pid=%d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseLsofPID(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestLookupChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	body := `0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  cc-clip_0.6.1_darwin_arm64.tar.gz
fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210  cc-clip_0.6.1_linux_amd64.tar.gz
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := lookupChecksum(path, "cc-clip_0.6.1_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}

	if _, err := lookupChecksum(path, "nonexistent.tar.gz"); err == nil {
		t.Error("expected error for missing entry, got nil")
	}
}

func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("hello, world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// printf 'hello, world\n' | shasum -a 256
	const want = "853ff93762a06ddbf722c4ebe9ddd66d8f63ddaea97f521c3ecc20da7c976020"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSamePath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "cc-clip")
	if err := os.WriteFile(real, []byte("#"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}
	if !samePath(real, link) {
		t.Errorf("samePath(real, symlink) = false, want true")
	}
	if samePath(real, filepath.Join(dir, "other")) {
		t.Errorf("samePath(real, other) = true, want false")
	}
}
