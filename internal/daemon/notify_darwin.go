//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// DarwinNotifier delivers macOS notifications with image thumbnails
// via terminal-notifier, falling back to osascript (text-only) if unavailable.
type DarwinNotifier struct {
	previewDir       string
	terminalNotifier string // path to terminal-notifier binary, empty if not found
}

// maxPreviewFiles limits the number of preview images retained on disk.
const maxPreviewFiles = 50

// previewCleanupInterval governs how often the background goroutine
// re-runs cleanupPreviews. 5 minutes balances responsiveness against
// wasted IO on idle daemons.
const previewCleanupInterval = 5 * time.Minute

func NewDarwinNotifier() *DarwinNotifier {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cache", "cc-clip", "previews")
	os.MkdirAll(dir, 0700)
	cleanupPreviews(dir, maxPreviewFiles)

	tn, _ := exec.LookPath("terminal-notifier")
	n := &DarwinNotifier{previewDir: dir, terminalNotifier: tn}

	// Background cleanup loop. The goroutine outlives any individual
	// Deliver call so cleanup never blocks the notification hot path,
	// and it does not need to be torn down explicitly because the
	// notifier is a process-singleton — the OS reaps the goroutine on
	// daemon exit. Tests that construct DarwinNotifier via a struct
	// literal (instead of NewDarwinNotifier) intentionally skip the
	// goroutine, keeping their behaviour deterministic.
	go n.runBackgroundCleanup(previewCleanupInterval)

	return n
}

// runBackgroundCleanup periodically prunes the preview cache. Replaces
// the per-Deliver synchronous cleanup that previously sat on the hot
// path. Runs until the process exits.
func (n *DarwinNotifier) runBackgroundCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		cleanupPreviews(n.previewDir, maxPreviewFiles)
	}
}

// cleanupPreviews removes the oldest preview files when the count exceeds max.
// Uses modification time for accurate ordering regardless of filename format.
func cleanupPreviews(dir string, max int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= max {
		return
	}
	type fileWithTime struct {
		name    string
		modTime int64
	}
	files := make([]fileWithTime, 0, len(entries))
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			files = append(files, fileWithTime{name: e.Name(), modTime: info.ModTime().UnixNano()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime < files[j].modTime })
	toRemove := len(files) - max
	for i := 0; i < toRemove; i++ {
		os.Remove(filepath.Join(dir, files[i].name))
	}
}

// platformDeliverer returns the darwin-specific notification adapter.
// Called by BuildDeliveryChain to add the macOS fallback.
func platformDeliverer() Deliverer {
	return NewDarwinNotifier()
}

// Name returns the adapter name for logging in the delivery chain.
func (n *DarwinNotifier) Name() string { return "darwin" }

// Deliver handles any envelope kind by rendering display text and sending
// via terminal-notifier (with image preview for image_transfer) or osascript.
func (n *DarwinNotifier) Deliver(_ context.Context, env NotifyEnvelope) error {
	title, body := formatNotification(env)
	sound := notificationSound(env)
	subtitle := ""
	imagePath := ""

	// For image transfers, write a preview thumbnail and build a subtitle
	if env.Kind == KindImageTransfer && env.ImageTransfer != nil {
		p := env.ImageTransfer
		subtitle = fmt.Sprintf("%s \u00b7 %dx%d \u00b7 %s", p.Fingerprint, p.Width, p.Height, p.Format)

		if len(p.ImageData) > 0 {
			ext := ".png"
			if p.Format == "jpeg" {
				ext = ".jpeg"
			}
			sid := p.SessionID
			if len(sid) > 8 {
				sid = sid[:8]
			}
			path := filepath.Join(n.previewDir, fmt.Sprintf("preview-%s-%d%s", sid, p.Seq, ext))
			if err := os.WriteFile(path, p.ImageData, 0600); err == nil {
				imagePath = path
				// Preview pruning intentionally runs in NewDarwinNotifier
				// (startup) and in Notify (the legacy event path), not here.
				// Doing it synchronously on the Deliver hot path adds
				// O(n log n) sort + N filesystem unlinks to every
				// notification, which hurts latency for long-running
				// daemons that have accumulated many previews.
			}
		}
	}

	// For generic / tool_attention envelopes, use subtitle from payload
	if env.GenericMessage != nil && env.GenericMessage.Subtitle != "" {
		subtitle = env.GenericMessage.Subtitle
	}

	if n.terminalNotifier != "" {
		return n.sendViaTerminalNotifier(title, subtitle, body, imagePath, sound)
	}
	return n.sendViaOsascript(title, subtitle, body, sound)
}

func (n *DarwinNotifier) Notify(_ context.Context, evt NotifyEvent) error {
	// Save preview image to disk
	var previewPath string
	if len(evt.ImageData) > 0 {
		ext := ".png"
		if evt.Format == "jpeg" {
			ext = ".jpeg"
		}
		sid := evt.SessionID
		if len(sid) > 8 {
			sid = sid[:8]
		}
		previewPath = filepath.Join(n.previewDir, fmt.Sprintf("preview-%s-%d%s", sid, evt.Seq, ext))
		if err := os.WriteFile(previewPath, evt.ImageData, 0600); err != nil {
			previewPath = ""
		} else {
			cleanupPreviews(n.previewDir, maxPreviewFiles)
		}
	}

	title := fmt.Sprintf("cc-clip #%d", evt.Seq)
	subtitle := fmt.Sprintf("%s · %dx%d · %s", evt.Fingerprint, evt.Width, evt.Height, evt.Format)

	body := "Image transferred"
	if evt.DuplicateOf > 0 {
		body = fmt.Sprintf("Duplicate of #%d", evt.DuplicateOf)
	}

	if n.terminalNotifier != "" {
		return n.sendViaTerminalNotifier(title, subtitle, body, previewPath, "")
	}
	return n.sendViaOsascript(title, subtitle, body, "")
}

func (n *DarwinNotifier) sendViaTerminalNotifier(title, subtitle, body, imagePath, sound string) error {
	args := []string{
		"-title", title,
		"-subtitle", subtitle,
		"-message", body,
		"-group", "cc-clip",
	}
	if sound != "" {
		args = append(args, "-sound", sound)
	}
	if imagePath != "" {
		args = append(args, "-contentImage", imagePath)
		args = append(args, "-open", "file://"+imagePath)
	}
	return exec.Command(n.terminalNotifier, args...).Run()
}

func (n *DarwinNotifier) sendViaOsascript(title, subtitle, body, sound string) error {
	script := fmt.Sprintf(`display notification %q with title %q subtitle %q`, body, title, subtitle)
	if sound != "" {
		script += fmt.Sprintf(` sound name %q`, sound)
	}
	return exec.Command("osascript", "-e", script).Run()
}
