package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHSessionFields(t *testing.T) {
	// Test that the struct properly stores host and controlPath.
	// We cannot test real SSH connections in unit tests, but we can verify
	// the accessor methods and struct construction.
	s := &SSHSession{
		host:        "testhost",
		controlPath: "/tmp/cc-clip-ssh-test",
	}

	if s.Host() != "testhost" {
		t.Errorf("expected host 'testhost', got %q", s.Host())
	}
	if s.ControlPath() != "/tmp/cc-clip-ssh-test" {
		t.Errorf("expected control path '/tmp/cc-clip-ssh-test', got %q", s.ControlPath())
	}
}

func TestParseUnameOutput(t *testing.T) {
	// Test the arch detection parsing logic that DetectRemoteArchViaSession uses.
	// We extract the parsing to verify it handles various uname outputs correctly.
	tests := []struct {
		name     string
		output   string
		wantOS   string
		wantArch string
		wantErr  bool
	}{
		{
			name:     "linux amd64",
			output:   "Linux x86_64",
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:     "linux arm64",
			output:   "Linux aarch64",
			wantOS:   "linux",
			wantArch: "arm64",
		},
		{
			name:     "darwin arm64",
			output:   "Darwin arm64",
			wantOS:   "darwin",
			wantArch: "arm64",
		},
		{
			name:     "darwin amd64",
			output:   "Darwin x86_64",
			wantOS:   "darwin",
			wantArch: "amd64",
		},
		{
			name:     "with trailing whitespace",
			output:   "  Linux  x86_64  \n",
			wantOS:   "linux",
			wantArch: "amd64",
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
		{
			name:    "single word",
			output:  "Linux",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goos, goarch, err := parseUnameOutput(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if goos != tt.wantOS {
				t.Errorf("OS: expected %q, got %q", tt.wantOS, goos)
			}
			if goarch != tt.wantArch {
				t.Errorf("arch: expected %q, got %q", tt.wantArch, goarch)
			}
		})
	}
}

func TestDetectRemoteArchParsing(t *testing.T) {
	// Verify the parsing logic matches what DetectRemoteArch and
	// DetectRemoteArchViaSession both use.
	// "Linux x86_64" -> linux, amd64
	goos, goarch, err := parseUnameOutput("Linux x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if goos != "linux" || goarch != "amd64" {
		t.Errorf("expected linux/amd64, got %s/%s", goos, goarch)
	}
}

func TestConnArgsWithControlPath(t *testing.T) {
	s := &SSHSession{
		host:        "myhost",
		controlPath: "/tmp/cc-clip-ssh-test",
	}
	args := s.connArgs()
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[0] != "-o" {
		t.Errorf("args[0] = %q, want '-o'", args[0])
	}
	if args[1] != "ControlPath=/tmp/cc-clip-ssh-test" {
		t.Errorf("args[1] = %q, want ControlPath=...", args[1])
	}
}

func TestConnArgsWithoutControlPath(t *testing.T) {
	// Windows path: controlPath is empty.
	s := &SSHSession{
		host:        "myhost",
		controlPath: "",
	}
	args := s.connArgs()
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[0] != "-o" {
		t.Errorf("args[0] = %q, want '-o'", args[0])
	}
	if args[1] != "ClearAllForwardings=yes" {
		t.Errorf("args[1] = %q, want 'ClearAllForwardings=yes'", args[1])
	}
}

func TestGenerateNotificationNonce(t *testing.T) {
	nonce, err := GenerateNotificationNonce()
	if err != nil {
		t.Fatalf("GenerateNotificationNonce failed: %v", err)
	}
	// 32 random bytes -> 64 hex characters
	if len(nonce) != 64 {
		t.Errorf("expected 64 hex chars, got %d: %q", len(nonce), nonce)
	}
	// Should be valid hex
	for _, c := range nonce {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("nonce contains non-hex character: %c", c)
		}
	}
}

func TestGenerateNotificationNonceUniqueness(t *testing.T) {
	nonce1, err := GenerateNotificationNonce()
	if err != nil {
		t.Fatal(err)
	}
	nonce2, err := GenerateNotificationNonce()
	if err != nil {
		t.Fatal(err)
	}
	if nonce1 == nonce2 {
		t.Fatal("two consecutive nonces should not be equal")
	}
}

func TestGenerateNotificationNonceDistinctFromSessionID(t *testing.T) {
	nonce, err := GenerateNotificationNonce()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := GenerateSessionID()
	if err != nil {
		t.Fatal(err)
	}
	// Nonce is 64 hex chars (32 bytes), session ID is 32 hex chars (16 bytes)
	if len(nonce) == len(sid) {
		t.Errorf("nonce and session ID should have different lengths: nonce=%d, sid=%d", len(nonce), len(sid))
	}
}

func TestCodexNotifyManagedBlockUsesConfigArray(t *testing.T) {
	block := codexNotifyManagedBlock("start", "end", 18339)
	if !strings.Contains(block, `notify = ["cc-clip", "notify", "--from-codex-stdin"]`) {
		t.Fatalf("expected notify array config, got %q", block)
	}
	if strings.Contains(block, "[notify]") {
		t.Fatalf("unexpected legacy [notify] table in %q", block)
	}
}

func TestCodexNotifyManagedBlockNonDefaultPort(t *testing.T) {
	block := codexNotifyManagedBlock("start", "end", 9999)
	if !strings.Contains(block, "CC_CLIP_PORT=9999") {
		t.Fatalf("expected CC_CLIP_PORT=9999 for non-default port, got %q", block)
	}
}

func TestRemoteHasCodex(t *testing.T) {
	s := &localSession{home: t.TempDir()}

	hasCodex, err := RemoteHasCodex(s)
	if err != nil {
		t.Fatalf("RemoteHasCodex returned error for missing ~/.codex: %v", err)
	}
	if hasCodex {
		t.Fatal("RemoteHasCodex should return false when ~/.codex is missing")
	}

	if err := os.Mkdir(filepath.Join(s.home, ".codex"), 0755); err != nil {
		t.Fatalf("failed to create .codex: %v", err)
	}
	hasCodex, err = RemoteHasCodex(s)
	if err != nil {
		t.Fatalf("RemoteHasCodex returned error: %v", err)
	}
	if !hasCodex {
		t.Fatal("RemoteHasCodex should return true when ~/.codex exists")
	}
}

func TestRemoteHasCodexReturnsExecError(t *testing.T) {
	_, err := RemoteHasCodex(&errorExecutor{err: fmt.Errorf("ssh failed")})
	if err == nil {
		t.Fatal("RemoteHasCodex should surface executor errors")
	}
}

func TestEnsureRemoteCodexNotifyConfigAppendsManagedBlock(t *testing.T) {
	s := &localSession{home: t.TempDir()}

	if err := EnsureRemoteCodexNotifyConfig(s, 9999); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if !strings.Contains(config, `notify = ["env", "CC_CLIP_PORT=9999", "cc-clip", "notify", "--from-codex-stdin"]`) {
		t.Fatalf("config missing managed notify block: %q", config)
	}
}

func TestEnsureRemoteCodexNotifyConfigRefusesUserNotify(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, `notify = ["custom", "notify"]`)

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err == nil {
		t.Fatal("EnsureRemoteCodexNotifyConfig should refuse an existing user notify setting")
	}
}

func TestEnsureRemoteCodexNotifyConfigReplacesManagedBlock(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "# before\n"+
		codexNotifyManagedBlock("# >>> cc-clip notify (do not edit) >>>", "# <<< cc-clip notify (do not edit) <<<", 18340)+
		"\n# after\n")

	if err := EnsureRemoteCodexNotifyConfig(s, 9999); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if strings.Count(config, "# >>> cc-clip notify (do not edit) >>>") != 1 {
		t.Fatalf("expected exactly one managed block, got config: %q", config)
	}
	if strings.Contains(config, "CC_CLIP_PORT=18340") {
		t.Fatalf("old managed block was not removed: %q", config)
	}
	if !strings.Contains(config, "CC_CLIP_PORT=9999") {
		t.Fatalf("new managed block was not appended: %q", config)
	}
}

func TestEnsureRemoteCodexNotifyConfigReturnsProbeError(t *testing.T) {
	err := EnsureRemoteCodexNotifyConfig(&errorExecutor{err: fmt.Errorf("ssh failed")}, 18339)
	if err == nil {
		t.Fatal("EnsureRemoteCodexNotifyConfig should surface probe executor errors")
	}
}

func TestEnsureRemoteCodexNotifyConfigStopsOnSedError(t *testing.T) {
	exec := &codexNotifySedErrorExecutor{}

	err := EnsureRemoteCodexNotifyConfig(exec, 18339)
	if err == nil {
		t.Fatal("EnsureRemoteCodexNotifyConfig should surface sed errors")
	}
	if exec.appended {
		t.Fatal("EnsureRemoteCodexNotifyConfig appended after sed failure")
	}
}

// parseUnameOutput is a testable extraction of the uname parsing logic.
// Both DetectRemoteArch and DetectRemoteArchViaSession use equivalent logic.
func parseUnameOutput(output string) (string, string, error) {
	parts := strings.Fields(strings.TrimSpace(output))
	if len(parts) < 2 {
		return "", "", fmt.Errorf("unexpected uname output: %s", output)
	}

	goos := strings.ToLower(parts[0])
	arch := parts[1]

	goarch := ""
	switch arch {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	default:
		goarch = arch
	}

	return goos, goarch, nil
}

func TestSessionExecutorInterface_StaticConformance(t *testing.T) {
	// Compile-time assertion: *SSHSession implements SessionExecutor.
	var _ SessionExecutor = (*SSHSession)(nil)
}

type codexNotifySedErrorExecutor struct {
	appended bool
}

func (c *codexNotifySedErrorExecutor) Exec(cmd string) (string, error) {
	if strings.Contains(cmd, "grep -F") {
		return "# >>> cc-clip notify (do not edit) >>>", nil
	}
	if strings.Contains(cmd, "sed -i") {
		return "", fmt.Errorf("sed failed")
	}
	if strings.Contains(cmd, "cat >>") {
		c.appended = true
	}
	return "", nil
}

func writeTestCodexConfig(t *testing.T, home, content string) {
	t.Helper()

	configDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create .codex dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}
}

func readTestCodexConfig(t *testing.T, home string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("failed to read test config: %v", err)
	}
	return string(data)
}
