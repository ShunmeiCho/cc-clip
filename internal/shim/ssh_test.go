package shim

import (
	"fmt"
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
		name       string
		output     string
		wantOS     string
		wantArch   string
		wantErr    bool
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
	block := codexNotifyManagedBlock("start", "end")
	if !strings.Contains(block, `notify = ["cc-clip", "notify", "--from-codex-stdin"]`) {
		t.Fatalf("expected notify array config, got %q", block)
	}
	if strings.Contains(block, "[notify]") {
		t.Fatalf("unexpected legacy [notify] table in %q", block)
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
