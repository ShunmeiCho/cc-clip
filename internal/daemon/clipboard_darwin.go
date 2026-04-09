//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// clipboardTimeout prevents clipboard operations from blocking the HTTP handler
// indefinitely (e.g. pbpaste on image clipboard can hang).
const clipboardTimeout = 5 * time.Second

// pngpasteFallbackPaths are checked when exec.LookPath fails, which happens
// in launchd environments where PATH doesn't include Homebrew directories.
var pngpasteFallbackPaths = []string{
	"/opt/homebrew/bin/pngpaste", // Apple Silicon Homebrew
	"/usr/local/bin/pngpaste",   // Intel Homebrew
}

type darwinClipboard struct{}

func NewClipboardReader() ClipboardReader {
	return &darwinClipboard{}
}

// findPngpaste resolves the pngpaste binary path.
// First tries LookPath (works in interactive shells), then falls back to
// well-known Homebrew paths (needed for launchd background service).
func findPngpaste() string {
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

func (c *darwinClipboard) Type() (ClipboardInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()

	// Try pngpaste first to detect image.
	// Discard stdout to prevent binary image data from leaking to the daemon's output.
	if pngpastePath := findPngpaste(); pngpastePath != "" {
		cmd := exec.CommandContext(ctx, pngpastePath, "-")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err == nil {
			return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
		}
	}

	// Check for text via pbpaste
	cmd := exec.CommandContext(ctx, "pbpaste")
	out, err := cmd.Output()
	if err != nil {
		return ClipboardInfo{Type: ClipboardEmpty}, nil
	}
	if len(out) > 0 {
		return ClipboardInfo{Type: ClipboardText}, nil
	}
	return ClipboardInfo{Type: ClipboardEmpty}, nil
}

func (c *darwinClipboard) ImageBytes() ([]byte, error) {
	pngpastePath := findPngpaste()
	if pngpastePath == "" {
		return nil, fmt.Errorf("pngpaste not found: install with 'brew install pngpaste'")
	}

	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, pngpastePath, "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("no image in clipboard: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("clipboard image is empty")
	}
	return out, nil
}
