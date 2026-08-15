//go:build darwin

package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

type darwinTextWriter struct{}

// NewClipboardTextWriter returns the macOS clipboard writer. pbcopy ships with
// the OS, so unlike pngpaste there is no install fallback to manage.
func NewClipboardTextWriter() ClipboardTextWriter {
	return &darwinTextWriter{}
}

func (w *darwinTextWriter) WriteText(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pbcopy failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
