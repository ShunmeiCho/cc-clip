package main

import (
	"io"
	"strings"
	"testing"
)

func TestParseSendArgsTreatsTrailingImagePathAsFileWithDefaultHost(t *testing.T) {
	opts, err := parseSendArgs([]string{"--paste", `C:\test.png`}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "" {
		t.Fatalf("expected host to remain default-resolved, got %q", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
	if !opts.paste {
		t.Fatal("expected --paste to be true")
	}
}

func TestParseSendArgsTreatsTrailingNonImageAsHost(t *testing.T) {
	opts, err := parseSendArgs([]string{"--paste", "myserver"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != "" {
		t.Fatalf("expected no localFile, got %q", opts.localFile)
	}
}

func TestParseSendArgsAllowsHostThenTrailingFile(t *testing.T) {
	opts, err := parseSendArgs([]string{"myserver", "--paste", `C:\test.png`}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
}

func TestParseSendArgsAllowsPostFlagHostThenFile(t *testing.T) {
	opts, err := parseSendArgs([]string{"--paste", "myserver", `C:\test.png`}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
}

func TestParseSendArgsAllowsFileThenFlagsWithDefaultHost(t *testing.T) {
	opts, err := parseSendArgs([]string{`C:\test.png`, "--paste"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "" {
		t.Fatalf("expected host to remain default-resolved, got %q", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
	if !opts.paste {
		t.Fatal("expected --paste to be true")
	}
}

func TestParseSendArgsAllowsHostAndFileThenFlags(t *testing.T) {
	opts, err := parseSendArgs([]string{"myserver", `C:\test.png`, "--paste"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
	if !opts.paste {
		t.Fatal("expected --paste to be true")
	}
}

func TestParseSendArgsRejectsFileFlagAndPositionalFile(t *testing.T) {
	_, err := parseSendArgs([]string{"--file", "a.png", "b.png"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot use both --file and positional image path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSendArgsRejectsExtraPositionalsWithHost(t *testing.T) {
	_, err := parseSendArgs([]string{"myserver", "--paste", "a.png", "b.png"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSendArgsRejectsFlagAfterPostFlagPositionals(t *testing.T) {
	_, err := parseSendArgs([]string{"--remote-dir", "uploads", `C:\test.png`, "--paste"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must appear before positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSendArgsRejectsNegativeDelay(t *testing.T) {
	_, err := parseSendArgs([]string{"--delay-ms", "-1"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --delay-ms") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestShQuoteNeutralizesShellMetacharacters pins shQuote's behavior so that
// scp remote-path hardening cannot silently regress. Every case would execute
// a shell command if shQuote is removed or weakened.
func TestShQuoteNeutralizesShellMetacharacters(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "foo.png", "'foo.png'"},
		{"space", "my file.png", "'my file.png'"},
		{"single quote", "foo's.png", `'foo'\''s.png'`},
		{"semicolon injection", "foo; rm -rf /.png", "'foo; rm -rf /.png'"},
		{"command substitution", "foo$(date).png", "'foo$(date).png'"},
		{"dollar variable", "$HOME/foo.png", "'$HOME/foo.png'"},
		{"backtick", "foo`date`.png", "'foo`date`.png'"},
		{"ampersand", "foo & bar.png", "'foo & bar.png'"},
		{"pipe", "foo|bar.png", "'foo|bar.png'"},
		{"newline", "foo\nbar.png", "'foo\nbar.png'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shQuote(tc.input)
			if got != tc.want {
				t.Fatalf("shQuote(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSSHUploadRemoteCmdQuotesPath pins the shape of the remote command used
// by sshUploadNoForward. Uploads now stream via `ssh host 'cat > <quoted>'`
// instead of `scp host:<path>`, because OpenSSH 9.0+ scp defaults to the SFTP
// subsystem where shell-quoting the remote side is interpreted literally and
// breaks transfer. This test ensures the remote path always lands inside a
// shell-quoted redirection target and that shell metacharacters are neutralized
// by shQuote rather than reaching the remote shell verbatim.
func TestSSHUploadRemoteCmdQuotesPath(t *testing.T) {
	cases := []struct {
		name       string
		remotePath string
		want       string
	}{
		{"plain", "/tmp/foo.png", "cat > '/tmp/foo.png'"},
		{"with space", "/tmp/my file.png", "cat > '/tmp/my file.png'"},
		{"semicolon injection", "/tmp/a; rm -rf /.png", "cat > '/tmp/a; rm -rf /.png'"},
		{"command substitution", "/tmp/$(id).png", "cat > '/tmp/$(id).png'"},
		{"single quote", "/tmp/foo's.png", `cat > '/tmp/foo'\''s.png'`},
		{"backtick", "/tmp/`date`.png", "cat > '/tmp/`date`.png'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sshUploadRemoteCmd(tc.remotePath)
			if got != tc.want {
				t.Fatalf("sshUploadRemoteCmd(%q) = %q, want %q", tc.remotePath, got, tc.want)
			}
		})
	}
}
