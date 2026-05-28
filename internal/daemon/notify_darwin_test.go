//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinNotifierDoesNotCleanupPreviewsOnDeliverHotPath(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxPreviewFiles+1; i++ {
		path := filepath.Join(dir, fmt.Sprintf("old-%02d.png", i))
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatalf("write old preview: %v", err)
		}
	}

	terminalNotifier := filepath.Join(dir, "terminal-notifier")
	if err := os.WriteFile(terminalNotifier, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake terminal-notifier: %v", err)
	}

	n := &DarwinNotifier{
		previewDir:       dir,
		terminalNotifier: terminalNotifier,
	}
	if err := n.Deliver(context.Background(), NotifyEnvelope{
		Kind:   KindImageTransfer,
		Source: "test",
		ImageTransfer: &ImageTransferPayload{
			SessionID:   "session-1234567890",
			Seq:         1,
			Fingerprint: "abcdef12",
			ImageData:   []byte("new image"),
			Format:      "png",
			Width:       1,
			Height:      1,
		},
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read preview dir: %v", err)
	}
	// Survival-check semantics: Deliver must not prune previews
	// synchronously. The dir was seeded with maxPreviewFiles+1 old
	// previews plus a fake terminal-notifier binary (53 files at this
	// point with one new preview). We assert a lower bound that
	// excludes pruning behaviour without depending on whether the
	// terminal-notifier binary is counted by os.ReadDir.
	minExpected := maxPreviewFiles + 2 // 51 seeded previews + 1 new preview
	if got := len(entries); got < minExpected {
		t.Fatalf("Deliver synchronously pruned previews; got %d files, want >= %d", got, minExpected)
	}
}
