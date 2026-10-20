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

// TestServiceFeaturesKeepsInstalledValue: re-registering the service without
// --features (setup, connect, update) must not silently re-enable anything.
func TestServiceFeaturesKeepsInstalledValue(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	t.Setenv("CC_CLIP_FEATURES", "")
	os.Args = []string{"cc-clip", "setup", "venus"}

	writeTestPlist(t, `<string>--features</string><string>image</string>`)
	if got := serviceFeatures(); got != (daemon.Features{Image: true}) {
		t.Fatalf("serviceFeatures() = %v, want installed image", got)
	}

	os.Args = []string{"cc-clip", "setup", "venus", "--features=all"}
	if got := serviceFeatures(); got != daemon.AllFeatures {
		t.Fatalf("explicit --features must win over the installed value, got %v", got)
	}

	os.Args = []string{"cc-clip", "setup", "venus"}
	writeTestPlist(t, `<string>--features</string><string>bogus</string>`)
	if got := serviceFeatures(); got != (daemon.Features{}) {
		t.Fatalf("unreadable installed features must fail closed to none, got %v", got)
	}

	writeTestPlist(t, ``)
	if got := serviceFeatures(); got != daemon.AllFeatures {
		t.Fatalf("no installed --features means the default (all), got %v", got)
	}

	// A plist that exists but cannot be parsed must not read as "no flag".
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", "com.cc-clip.daemon.plist"), []byte("bplist00garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := serviceFeatures(); got != (daemon.Features{}) {
		t.Fatalf("unparsable plist must fail closed to none, got %v", got)
	}

	// No plist at all is a fresh install: the default applies.
	t.Setenv("HOME", t.TempDir())
	if got := serviceFeatures(); got != daemon.AllFeatures {
		t.Fatalf("no service installed means the default (all), got %v", got)
	}
}
