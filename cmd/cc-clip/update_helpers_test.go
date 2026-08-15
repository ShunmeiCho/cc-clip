package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseUpdateFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want updateOptions
	}{
		{"no flags", nil, updateOptions{}},
		{"check only", []string{"--check"}, updateOptions{check: true}},
		{"force and target", []string{"--force", "--to", "v0.6.0"}, updateOptions{force: true, toVer: "v0.6.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseUpdateFlags(tt.args); got != tt.want {
				t.Fatalf("parseUpdateFlags(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDisplayVersion(t *testing.T) {
	t.Parallel()
	if got := displayVersion(""); !strings.Contains(got, "dev build") {
		t.Fatalf("empty version must be labeled a dev build: %q", got)
	}
	if got := displayVersion("v0.9.3"); got != "v0.9.3" {
		t.Fatalf("real version must pass through: %q", got)
	}
}

func TestDescribeServiceState(t *testing.T) {
	t.Parallel()
	running, stopped := describeServiceState(true), describeServiceState(false)
	if runtime.GOOS == "darwin" {
		if !strings.Contains(running, "running") || !strings.Contains(stopped, "not running") {
			t.Fatalf("darwin states: running=%q stopped=%q", running, stopped)
		}
		return
	}
	if !strings.Contains(running, "not managed") {
		t.Fatalf("non-darwin must report unmanaged: %q", running)
	}
}

// writeChecksums writes a goreleaser-style checksums.txt ("<hash>  <name>").
func writeChecksums(t *testing.T, dir string, entries map[string]string) string {
	t.Helper()
	var b strings.Builder
	for name, hash := range entries {
		fmt.Fprintf(&b, "%s  %s\n", hash, name)
	}
	path := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifySHA256(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "cc-clip_0.9.3_darwin_arm64.tar.gz")
	content := []byte("archive-bytes")
	if err := os.WriteFile(archive, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	t.Run("match", func(t *testing.T) {
		checksums := writeChecksums(t, t.TempDir(), map[string]string{"cc-clip_0.9.3_darwin_arm64.tar.gz": good})
		if err := verifySHA256(archive, checksums, "cc-clip_0.9.3_darwin_arm64.tar.gz"); err != nil {
			t.Fatalf("verifySHA256: %v", err)
		}
	})
	t.Run("mismatch is rejected", func(t *testing.T) {
		checksums := writeChecksums(t, t.TempDir(), map[string]string{"cc-clip_0.9.3_darwin_arm64.tar.gz": strings.Repeat("0", 64)})
		if err := verifySHA256(archive, checksums, "cc-clip_0.9.3_darwin_arm64.tar.gz"); err == nil || !strings.Contains(err.Error(), "mismatch") {
			t.Fatalf("want checksum mismatch, got %v", err)
		}
	})
	t.Run("missing entry is rejected", func(t *testing.T) {
		checksums := writeChecksums(t, t.TempDir(), map[string]string{"other.tar.gz": good})
		if err := verifySHA256(archive, checksums, "cc-clip_0.9.3_darwin_arm64.tar.gz"); err == nil {
			t.Fatal("want error for archive absent from checksums.txt")
		}
	})
}

// writeTarGz builds a tar.gz with the given name->content entries.
func writeTarGz(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractBinary(t *testing.T) {
	t.Parallel()

	t.Run("finds cc-clip among other entries", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "rel.tar.gz")
		writeTarGz(t, archive, map[string]string{
			"README.md":    "docs",
			"dist/cc-clip": "binary-bytes",
			"dist/LICENSE": "license",
		})
		dest, cleanup, err := extractBinary(archive)
		if err != nil {
			t.Fatalf("extractBinary: %v", err)
		}
		defer cleanup()
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "binary-bytes" {
			t.Fatalf("extracted content = %q", got)
		}
		if info, _ := os.Stat(dest); info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("extracted binary must be executable, mode=%v", info.Mode())
		}
	})

	t.Run("archive without the binary errors", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "rel.tar.gz")
		writeTarGz(t, archive, map[string]string{"README.md": "docs"})
		_, cleanup, err := extractBinary(archive)
		defer cleanup()
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("want not-found error, got %v", err)
		}
	})
}

func TestRenameAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := renameAtomic(src, dst); err != nil {
		t.Fatalf("renameAtomic: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "payload" {
		t.Fatalf("dst content = %q, err=%v", got, err)
	}
	if err := renameAtomic(filepath.Join(dir, "missing"), dst); err == nil {
		t.Fatal("missing source must error")
	}
}

func TestResolveSelfPath(t *testing.T) {
	t.Parallel()
	p, err := resolveSelfPath()
	if err != nil {
		t.Fatalf("resolveSelfPath: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("path must be absolute: %q", p)
	}
}
