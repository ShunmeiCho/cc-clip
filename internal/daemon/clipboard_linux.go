//go:build linux

package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

type linuxClipboard struct{}

func NewClipboardReader() ClipboardReader {
	return &linuxClipboard{}
}

func (c *linuxClipboard) Type() (ClipboardInfo, error) {
	// Try xclip first
	if xclipPath, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command(xclipPath, "-selection", "clipboard", "-t", "TARGETS", "-o")
		out, err := cmd.Output()
		if err == nil {
			targets := string(out)
			if strings.Contains(targets, "image/png") {
				return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
			}
			if strings.Contains(targets, "image/jpeg") {
				return ClipboardInfo{Type: ClipboardImage, Format: "jpeg"}, nil
			}
			if strings.Contains(targets, "UTF8_STRING") || strings.Contains(targets, "TEXT") {
				return ClipboardInfo{Type: ClipboardText}, nil
			}
		}
	}

	// Try wl-paste for Wayland
	if wlPath, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command(wlPath, "--list-types")
		out, err := cmd.Output()
		if err == nil {
			types := string(out)
			if strings.Contains(types, "image/png") {
				return ClipboardInfo{Type: ClipboardImage, Format: "png"}, nil
			}
			if strings.Contains(types, "image/jpeg") {
				return ClipboardInfo{Type: ClipboardImage, Format: "jpeg"}, nil
			}
			if strings.Contains(types, "text/plain") {
				return ClipboardInfo{Type: ClipboardText}, nil
			}
		}
	}

	return ClipboardInfo{Type: ClipboardEmpty}, nil
}

func (c *linuxClipboard) ImageBytes() ([]byte, error) {
	// Try xclip
	if xclipPath, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command(xclipPath, "-selection", "clipboard", "-t", "image/png", "-o")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}

	// Try wl-paste
	if wlPath, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command(wlPath, "--type", "image/png")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}

	return nil, fmt.Errorf("no image in clipboard: xclip or wl-paste required")
}
