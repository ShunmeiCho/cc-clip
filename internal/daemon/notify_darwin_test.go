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

func TestBuildTerminalNotifierArgs(t *testing.T) {
	t.Run("sound, appIcon and contentImage included when set", func(t *testing.T) {
		args := buildTerminalNotifierArgs("T", "S", "B", "/tmp/p.png", "Ping", "/tmp/icon.png")
		assertArgPair(t, args, "-title", "T")
		assertArgPair(t, args, "-sound", "Ping")
		assertArgPair(t, args, "-appIcon", "/tmp/icon.png")
		assertArgPair(t, args, "-contentImage", "/tmp/p.png")
	})
	t.Run("optional flags omitted when empty", func(t *testing.T) {
		args := buildTerminalNotifierArgs("T", "S", "B", "", "", "")
		for _, flag := range []string{"-sound", "-appIcon", "-contentImage"} {
			if containsArg(args, flag) {
				t.Fatalf("expected no %s flag, got %v", flag, args)
			}
		}
	})
}

func TestNotifyAppIcon(t *testing.T) {
	t.Run("unset returns empty", func(t *testing.T) {
		t.Setenv("CC_CLIP_NOTIFY_APP_ICON", "")
		if got := notifyAppIcon(); got != "" {
			t.Fatalf("unset icon should be empty, got %q", got)
		}
	})
	t.Run("nonexistent path is ignored", func(t *testing.T) {
		t.Setenv("CC_CLIP_NOTIFY_APP_ICON", filepath.Join(t.TempDir(), "missing.png"))
		if got := notifyAppIcon(); got != "" {
			t.Fatalf("missing icon path should be ignored, got %q", got)
		}
	})
	t.Run("directory path is ignored", func(t *testing.T) {
		t.Setenv("CC_CLIP_NOTIFY_APP_ICON", t.TempDir())
		if got := notifyAppIcon(); got != "" {
			t.Fatalf("directory should be ignored, got %q", got)
		}
	})
	t.Run("existing file path is returned", func(t *testing.T) {
		icon := filepath.Join(t.TempDir(), "icon.png")
		if err := os.WriteFile(icon, []byte("png"), 0600); err != nil {
			t.Fatalf("write icon: %v", err)
		}
		t.Setenv("CC_CLIP_NOTIFY_APP_ICON", icon)
		if got := notifyAppIcon(); got != icon {
			t.Fatalf("existing icon should be returned, got %q want %q", got, icon)
		}
	})
}

// containsArg reports whether flag appears as a standalone token in args.
func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// assertArgPair asserts that flag is immediately followed by value in args.
func assertArgPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			if args[i+1] != value {
				t.Fatalf("flag %s = %q, want %q (args=%v)", flag, args[i+1], value, args)
			}
			return
		}
	}
	t.Fatalf("flag %s not found in args=%v", flag, args)
}
