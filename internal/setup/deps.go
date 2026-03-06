package setup

import (
	"fmt"
	"os"
	"os/exec"
)

var pngpasteFallbackPaths = []string{
	"/opt/homebrew/bin/pngpaste", // Apple Silicon Homebrew
	"/usr/local/bin/pngpaste",   // Intel Homebrew
}

// CheckPngpaste checks if pngpaste is available.
// Returns the path if found, empty string if not.
func CheckPngpaste() string {
	if p, err := exec.LookPath("pngpaste"); err == nil {
		return p
	}
	for _, p := range pngpasteFallbackPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// InstallPngpaste installs pngpaste via Homebrew.
func InstallPngpaste() error {
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("Homebrew not found; install pngpaste manually: brew install pngpaste")
	}
	cmd := exec.Command("brew", "install", "pngpaste")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("brew install pngpaste failed: %w", err)
	}
	return nil
}
