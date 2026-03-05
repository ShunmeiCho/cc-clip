//go:build darwin

package daemon

import (
	"fmt"
	"os/exec"
)

type darwinClipboard struct{}

func NewClipboardReader() ClipboardReader {
	return &darwinClipboard{}
}

func (c *darwinClipboard) Type() (ClipboardInfo, error) {
	// Try pngpaste first to detect image
	if pngpastePath, err := exec.LookPath("pngpaste"); err == nil {
		cmd := exec.Command(pngpastePath, "-")
		if err := cmd.Run(); err == nil {
			return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
		}
	}

	// Check for text via pbpaste
	cmd := exec.Command("pbpaste")
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
	pngpastePath, err := exec.LookPath("pngpaste")
	if err != nil {
		return nil, fmt.Errorf("pngpaste not found: install with 'brew install pngpaste'")
	}

	cmd := exec.Command(pngpastePath, "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("no image in clipboard: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("clipboard image is empty")
	}
	return out, nil
}
