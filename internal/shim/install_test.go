package shim

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallAndUninstallXclip(t *testing.T) {
	dir := t.TempDir()

	result, err := Install(TargetXclip, dir, 18339)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	if result.Target != TargetXclip {
		t.Fatalf("expected target xclip, got %s", result.Target)
	}
	if result.ShimPath != filepath.Join(dir, "xclip") {
		t.Fatalf("unexpected shim path: %s", result.ShimPath)
	}

	// Verify shim file exists and is executable
	info, err := os.Stat(result.ShimPath)
	if err != nil {
		t.Fatalf("shim file not found: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Fatal("shim file is not executable")
	}

	// Verify shim content
	data, err := os.ReadFile(result.ShimPath)
	if err != nil {
		t.Fatalf("failed to read shim: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "cc-clip") {
		t.Fatal("shim does not contain cc-clip marker")
	}
	if !strings.Contains(content, "18339") {
		t.Fatal("shim does not contain expected port")
	}

	// Test duplicate install fails
	_, err = Install(TargetXclip, dir, 18339)
	if err == nil {
		t.Fatal("expected error on duplicate install")
	}

	// Uninstall
	if err := Uninstall(TargetXclip, dir); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	// Verify removed
	if _, err := os.Stat(result.ShimPath); !os.IsNotExist(err) {
		t.Fatal("shim file should be removed after uninstall")
	}
}

func TestUninstallNonShim(t *testing.T) {
	dir := t.TempDir()

	// Create a non-shim file
	path := filepath.Join(dir, "xclip")
	os.WriteFile(path, []byte("#!/bin/bash\necho real xclip"), 0755)

	err := Uninstall(TargetXclip, dir)
	if err == nil {
		t.Fatal("expected error when uninstalling non-shim file")
	}
}

func TestInstallWlPaste(t *testing.T) {
	dir := t.TempDir()

	result, err := Install(TargetWlPaste, dir, 18339)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	if result.Target != TargetWlPaste {
		t.Fatalf("expected target wl-paste, got %s", result.Target)
	}

	data, _ := os.ReadFile(result.ShimPath)
	if !strings.Contains(string(data), "wl-paste") {
		t.Fatal("wl-paste shim content incorrect")
	}

	Uninstall(TargetWlPaste, dir)
}

func TestIsOurShim(t *testing.T) {
	dir := t.TempDir()

	// Not our shim
	path := filepath.Join(dir, "xclip")
	os.WriteFile(path, []byte("#!/bin/bash\necho real"), 0755)
	if isOurShim(path) {
		t.Fatal("should not detect non-shim as our shim")
	}

	// Our shim
	os.WriteFile(path, []byte("#!/bin/bash\n# cc-clip shim"), 0755)
	if !isOurShim(path) {
		t.Fatal("should detect our shim")
	}

	// Non-existent
	if isOurShim(filepath.Join(dir, "nonexistent")) {
		t.Fatal("should not detect non-existent as our shim")
	}
}

func TestXclipShimContent(t *testing.T) {
	content := XclipShim(18339, "/usr/bin/xclip")

	checks := []string{
		"#!/bin/bash",
		"cc-clip",
		"18339",
		"/usr/bin/xclip",
		"TARGETS",
		"image/",
		"_cc_clip_fallback",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("shim missing expected content: %q", check)
		}
	}
}
