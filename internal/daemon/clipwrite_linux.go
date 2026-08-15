//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type linuxTextWriter struct{}

// NewClipboardTextWriter returns the Linux clipboard writer. Tool selection
// mirrors NewClipboardReader: Wayland sessions prefer wl-copy, X11 falls back
// to xclip. Resolution happens per write, not at construction, so a tool
// installed after the daemon started is picked up.
func NewClipboardTextWriter() ClipboardTextWriter {
	return &linuxTextWriter{}
}

func (w *linuxTextWriter) WriteText(text string) error {
	var cmd *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "" && lookPathOK("wl-copy"):
		cmd = exec.Command("wl-copy")
	case lookPathOK("xclip"):
		cmd = exec.Command("xclip", "-selection", "clipboard", "-i")
	case lookPathOK("wl-copy"):
		cmd = exec.Command("wl-copy")
	default:
		return fmt.Errorf("no clipboard write tool found: install xclip or wl-clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s failed: %s: %w", cmd.Path, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func lookPathOK(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
