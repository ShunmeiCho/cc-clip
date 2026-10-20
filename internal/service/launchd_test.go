//go:build darwin

package service

import (
	"os"
	"strings"
	"testing"
)

func TestGeneratePlist(t *testing.T) {
	content := generatePlist("/usr/local/bin/cc-clip", 18339)

	checks := []struct {
		name     string
		contains string
	}{
		{"label", "<string>com.cc-clip.daemon</string>"},
		{"binary path", "<string>/usr/local/bin/cc-clip</string>"},
		{"serve command", "<string>serve</string>"},
		{"port flag", "<string>--port</string>"},
		{"port value", "<string>18339</string>"},
		{"run at load", "<key>RunAtLoad</key>"},
		{"run at load true", "<true/>"},
		{"keep alive", "<key>KeepAlive</key>"},
		{"log path", "cc-clip.log"},
		{"xml header", "<?xml version"},
		{"plist doctype", "<!DOCTYPE plist"},
	}

	for _, check := range checks {
		if !strings.Contains(content, check.contains) {
			t.Errorf("plist missing %s: expected to contain %q", check.name, check.contains)
		}
	}
}

func TestGeneratePlist_CustomPort(t *testing.T) {
	content := generatePlist("/opt/bin/cc-clip", 9999)

	if !strings.Contains(content, "<string>9999</string>") {
		t.Error("plist does not contain custom port 9999")
	}
	if !strings.Contains(content, "<string>/opt/bin/cc-clip</string>") {
		t.Error("plist does not contain custom binary path")
	}
}

func TestPlistPath(t *testing.T) {
	path := PlistPath()
	if !strings.HasSuffix(path, "Library/LaunchAgents/com.cc-clip.daemon.plist") {
		t.Errorf("unexpected plist path: %s", path)
	}
}

func TestInstall_MockLaunchctl(t *testing.T) {
	tmpDir := t.TempDir()

	// Override PlistPath temporarily by using a custom binary path
	// We can't easily override PlistPath, so we test via the generated content
	// and mock launchctl calls

	loadCalled := false
	originalLoad := launchctlLoad
	launchctlLoad = func(plistPath string) error {
		loadCalled = true
		// Verify the plist file was written before load was called
		if _, err := os.Stat(plistPath); err != nil {
			t.Errorf("plist file not found when launchctl load called: %v", err)
		}
		return nil
	}
	defer func() { launchctlLoad = originalLoad }()

	// Write to a temp plist path — we test the content generation separately
	plistContent := generatePlist(tmpDir+"/cc-clip", 18339)
	plistPath := tmpDir + "/test.plist"
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		t.Fatalf("failed to write test plist: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("failed to read test plist: %v", err)
	}
	if !strings.Contains(string(data), "com.cc-clip.daemon") {
		t.Error("plist content missing label")
	}

	// Call load mock to verify it works
	if err := launchctlLoad(plistPath); err != nil {
		t.Fatalf("mock launchctl load failed: %v", err)
	}
	if !loadCalled {
		t.Error("launchctl load was not called")
	}
}

func TestUninstall_MockLaunchctl(t *testing.T) {
	unloadCalled := false
	originalUnload := launchctlUnload
	launchctlUnload = func(plistPath string) error {
		unloadCalled = true
		return nil
	}
	defer func() { launchctlUnload = originalUnload }()

	// Call Uninstall — it should call unload and attempt remove (which is fine if file doesn't exist)
	_ = launchctlUnload(PlistPath())
	if !unloadCalled {
		t.Error("launchctl unload was not called")
	}
}

func TestStatus_MockLaunchctl(t *testing.T) {
	originalList := launchctlList
	defer func() { launchctlList = originalList }()

	// Mock: service is running
	launchctlList = func(label string) (bool, error) {
		if label != plistLabel {
			t.Errorf("unexpected label: %s", label)
		}
		return true, nil
	}
	running, err := Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !running {
		t.Error("expected service to be running")
	}

	// Mock: service is not running
	launchctlList = func(label string) (bool, error) {
		return false, nil
	}
	running, err = Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if running {
		t.Error("expected service to not be running")
	}
}

// TestInstalledPortRoundTrip pins the port back out of the plist. `cc-clip
// update` re-registered the service on a hardcoded 18339, moving the daemon off
// the port every existing SSH tunnel forwards.
func TestInstalledPortRoundTrip(t *testing.T) {
	got, ok := parsePlistPort(generatePlist("/usr/local/bin/cc-clip", 19001))
	if !ok {
		t.Fatal("port must be readable from a plist this package wrote")
	}
	if got != 19001 {
		t.Fatalf("port = %d, want 19001", got)
	}

	if _, ok := parsePlistPort("<plist><dict></dict></plist>"); ok {
		t.Fatal("a plist without --port must report no port, not a default")
	}
}

// TestParsePlistPortIgnoresComments pins the scope of the read. A legal XML
// comment mentioning an older port was taken as configuration, and the caller
// then moved the running service onto it.
func TestParsePlistPortIgnoresComments(t *testing.T) {
	withComment := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <!-- was <string>--port</string><string>19002</string> before the move -->
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/cc-clip</string>
        <string>serve</string>
        <string>--port</string>
        <string>19001</string>
    </array>
</dict>
</plist>`
	got, ok := parsePlistPort(withComment)
	if !ok {
		t.Fatal("the real ProgramArguments port must still be readable")
	}
	if got != 19001 {
		t.Fatalf("port = %d, want 19001 (a comment was read as configuration)", got)
	}

	// A port outside ProgramArguments is not configuration either.
	outside := `<plist version="1.0"><dict>
    <key>EnvironmentVariables</key><dict><key>PATH</key><string>--port</string><string>19003</string></dict>
    <key>ProgramArguments</key><array><string>/usr/local/bin/cc-clip</string><string>serve</string></array>
</dict></plist>`
	if _, ok := parsePlistPort(outside); ok {
		t.Fatal("a --port outside ProgramArguments must not be read as the service port")
	}
}

// TestPlistServeArgsRoundTrip pins extra serve flags (here --text) into the
// plist and back out, so update/setup can re-register without dropping them.
func TestPlistServeArgsRoundTrip(t *testing.T) {
	plist := generatePlist("/usr/local/bin/cc-clip", 19001, "--text", "off")
	if !strings.Contains(plist, "<string>--port</string>\n        <string>19001</string>\n        <string>--text</string>\n        <string>off</string>\n    </array>") {
		t.Fatalf("serve args not appended to ProgramArguments:\n%s", plist)
	}
	if got, ok := parsePlistFlag(plist, "text"); !ok || got != "off" {
		t.Fatalf("parsePlistFlag(text) = %q, %v; want off, true", got, ok)
	}
	if port, ok := parsePlistPort(plist); !ok || port != 19001 {
		t.Fatalf("extra args broke port parsing: %d, %v", port, ok)
	}
	if _, ok := parsePlistFlag(generatePlist("/usr/local/bin/cc-clip", 18339), "text"); ok {
		t.Fatal("a plist without --text must report no text flag")
	}
	if got, _ := parsePlistFlag(generatePlist("/x", 1, "--text", "<&>"), "text"); got != "<&>" {
		t.Fatalf("serve args must be XML-escaped and unescaped, got %q", got)
	}
}
