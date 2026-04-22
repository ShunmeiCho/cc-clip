package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestReleaseArchiveName pins the exact filename the updater expects at
// `<release>/cc-clip_<version>_<os>_<arch>.tar.gz`. If this shape ever
// changes in goreleaser or scripts/install.sh without being reflected here
// (or vice versa), the matching `make release-preflight` grep ensures the
// drift is caught before a tag ships.
func TestReleaseArchiveName(t *testing.T) {
	cases := []struct {
		name   string
		tag    string
		goos   string
		goarch string
		want   string
	}{
		{"darwin arm64", "v0.6.2", "darwin", "arm64", "cc-clip_0.6.2_darwin_arm64.tar.gz"},
		{"linux amd64", "v0.6.2", "linux", "amd64", "cc-clip_0.6.2_linux_amd64.tar.gz"},
		{"linux arm64", "v0.6.2", "linux", "arm64", "cc-clip_0.6.2_linux_arm64.tar.gz"},
		{"darwin amd64", "v0.6.2", "darwin", "amd64", "cc-clip_0.6.2_darwin_amd64.tar.gz"},
		{"tag without v prefix", "0.6.2", "darwin", "arm64", "cc-clip_0.6.2_darwin_arm64.tar.gz"},
		{"older release", "v0.5.0", "linux", "amd64", "cc-clip_0.5.0_linux_amd64.tar.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseArchiveName(tc.tag, tc.goos, tc.goarch)
			if got != tc.want {
				t.Errorf("releaseArchiveName(%q, %q, %q) = %q, want %q",
					tc.tag, tc.goos, tc.goarch, got, tc.want)
			}
		})
	}
}

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

		// git-describe output between tags (real dev-build ldflags value)
		// must not be treated as a release version; otherwise the updater
		// reports "already at target" and refuses to pull the real release.
		{"v0.6.1-1-g4d2038b", ""},
		{"v0.6.1-5-gabc1234", ""},
		{"0.6.1-1-g4d2038b", ""},

		// dirty-tree markers that `git describe --dirty` appends
		{"v0.6.1-dirty", ""},
		{"v0.6.1-1-g4d2038b-dirty", ""},
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

func TestStagedVersionMatches(t *testing.T) {
	cases := []struct {
		name     string
		reported string
		target   string
		want     bool
	}{
		{"exact", "cc-clip 0.6.1", "v0.6.1", true},
		{"no prefix in target", "cc-clip 0.6.1", "0.6.1", true},
		{"v-prefix in reported", "cc-clip v0.6.1", "v0.6.1", true},
		{"whitespace", "  cc-clip 0.6.1\n", "v0.6.1", true},
		{"mismatch", "cc-clip 0.6.0", "v0.6.1", false},
		{"empty reported", "", "v0.6.1", false},
		{"dev build reported", "cc-clip dev", "v0.6.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stagedVersionMatches(tc.reported, tc.target)
			if got != tc.want {
				t.Errorf("stagedVersionMatches(%q, %q) = %v, want %v", tc.reported, tc.target, got, tc.want)
			}
		})
	}
}

func TestParseLsofTextPath(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "empty",
			raw:  "",
			want: "",
		},
		{
			name: "single absolute path",
			raw:  "p12345\nftxt\nn/Users/shunmei/.local/bin/cc-clip\n",
			want: "/Users/shunmei/.local/bin/cc-clip",
		},
		{
			name: "first absolute path wins",
			raw: `p12345
ftxt
n/Users/shunmei/.local/bin/cc-clip
ftxt
n/usr/lib/libSystem.B.dylib
`,
			want: "/Users/shunmei/.local/bin/cc-clip",
		},
		{
			name: "skip non-n lines and non-absolute n lines",
			raw: `p12345
ftxt
n(noroot)
n/Users/shunmei/.local/bin/cc-clip
`,
			want: "/Users/shunmei/.local/bin/cc-clip",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLsofTextPath(tc.raw)
			if got != tc.want {
				t.Errorf("parseLsofTextPath(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunVersionCommandTimeout guarantees that a binary that hangs on
// `--version` cannot wedge the updater. Without a timeout this path would
// block forever AFTER the binary swap had already happened, meaning no
// rollback could ever run. We simulate the hang with a shell script that
// sleeps for 30s and verify the call returns within 2s with an error.
func TestRunVersionCommandTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub does not run on Windows")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "fake-cc-clip")
	// Stub ignores its argv and sleeps, so `--version` will never return.
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := runVersionCommandWithTimeout(stub, 500*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected non-nil error when --version exceeds timeout")
	}
	// Ceiling = timeout (500ms) + WaitDelay (1s) + scheduling slack.
	if elapsed > 3*time.Second {
		t.Errorf("runVersionCommandWithTimeout took %s, want < 3s (timeout not enforced)", elapsed)
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
