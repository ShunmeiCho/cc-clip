//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shunmei/cc-clip/internal/daemon"
)

func writeTestPlist(t *testing.T, args string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<plist><dict><key>ProgramArguments</key><array>` +
		`<string>/x/cc-clip</string><string>serve</string><string>--port</string><string>18339</string>` +
		args + `</array></dict></plist>`
	if err := os.WriteFile(filepath.Join(dir, "com.cc-clip.daemon.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestServiceTextModeKeepsInstalledValue: re-registering the service without
// --text (setup, connect, update) must not silently re-enable text.
func TestServiceTextModeKeepsInstalledValue(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	t.Setenv("CC_CLIP_TEXT", "")
	os.Args = []string{"cc-clip", "setup", "venus"}

	writeTestPlist(t, `<string>--text</string><string>off</string>`)
	if got := serviceTextMode(); got != (daemon.TextMode{}) {
		t.Fatalf("serviceTextMode() = %v, want installed off", got)
	}

	os.Args = []string{"cc-clip", "setup", "venus", "--text=both"}
	if got := serviceTextMode(); got != daemon.TextModeBoth {
		t.Fatalf("explicit --text must win over the installed value, got %v", got)
	}

	os.Args = []string{"cc-clip", "setup", "venus"}
	writeTestPlist(t, `<string>--text</string><string>bogus</string>`)
	if got := serviceTextMode(); got != (daemon.TextMode{}) {
		t.Fatalf("unreadable installed mode must fail closed to off, got %v", got)
	}

	writeTestPlist(t, ``)
	if got := serviceTextMode(); got != daemon.TextModeBoth {
		t.Fatalf("no installed --text means the default (both), got %v", got)
	}
}
