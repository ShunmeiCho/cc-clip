package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/shim"
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
// remote-path hardening for mkdir -p and the ssh-stdin upload command cannot
// silently regress. Every case would execute a shell command if shQuote is
// removed or weakened.
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
// by sshUploadNoForward. Uploads stream via `ssh host 'umask 077; cat > <quoted>'`
// instead of `scp host:<path>`, because OpenSSH 9.0+ scp defaults to the SFTP
// subsystem where shell-quoting the remote side is interpreted literally and
// breaks transfer. This test ensures:
//
//  1. The remote path always lands inside a shell-quoted redirection target and
//     shell metacharacters are neutralized by shQuote rather than reaching the
//     remote shell verbatim.
//  2. The `umask 077;` prefix stays in place so uploaded clipboard images are
//     created as 0600 regardless of the remote account's default umask.
func TestSSHUploadRemoteCmdQuotesPath(t *testing.T) {
	cases := []struct {
		name       string
		remotePath string
		want       string
	}{
		{"plain", "/tmp/foo.png", "umask 077; cat > '/tmp/foo.png'"},
		{"with space", "/tmp/my file.png", "umask 077; cat > '/tmp/my file.png'"},
		{"semicolon injection", "/tmp/a; rm -rf /.png", "umask 077; cat > '/tmp/a; rm -rf /.png'"},
		{"command substitution", "/tmp/$(id).png", "umask 077; cat > '/tmp/$(id).png'"},
		{"single quote", "/tmp/foo's.png", `umask 077; cat > '/tmp/foo'\''s.png'`},
		{"backtick", "/tmp/`date`.png", "umask 077; cat > '/tmp/`date`.png'"},
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

func TestRemoteUploadSizeCmdQuotesPathAndRejectsEmpty(t *testing.T) {
	remotePath := "/tmp/a; rm -rf /.png"
	got := remoteUploadSizeCmd(remotePath)
	want := "sh -lc 'test -s '\\''/tmp/a; rm -rf /.png'\\'' && wc -c < '\\''/tmp/a; rm -rf /.png'\\'''"
	if got != want {
		t.Fatalf("remoteUploadSizeCmd(%q) = %q, want %q", remotePath, got, want)
	}
}

func TestParseRemoteUploadSize(t *testing.T) {
	for _, input := range []string{
		"12345\n",
		"zshenv banner\n12345\n",
		"debug 2026\n   12345\n",
	} {
		got, err := parseRemoteUploadSize(input)
		if err != nil {
			t.Fatalf("parseRemoteUploadSize(%q) returned error: %v", input, err)
		}
		if got != 12345 {
			t.Fatalf("parseRemoteUploadSize(%q) = %d, want 12345", input, got)
		}
	}
	if _, err := parseRemoteUploadSize(""); err == nil {
		t.Fatal("expected empty size output to fail")
	}
	if _, err := parseRemoteUploadSize("no byte count here"); err == nil {
		t.Fatal("expected non-numeric size output to fail")
	}
}

// TestParseRemoteHomeStripsSSHWarningPollution reproduces issue #80. Windows
// OpenSSH emits a post-quantum key-exchange banner on every connection. Under
// the old CombinedOutput()-based remote home lookup, that banner was
// concatenated into $HOME, so the remote upload path became
// "/root/** WARNING.../root/.cache/cc-clip/uploads/clip-*.png" and the file
// landed at a wrong, deeply nested path while `send` still exited 0. Wrapping
// the value in sentinels and extracting only the text between them must yield
// the clean home regardless of any banner lines before or after it.
func TestParseRemoteHomeStripsSSHWarningPollution(t *testing.T) {
	pqBanner := "** WARNING: connection is not using a post-quantum key exchange algorithm.\n" +
		"** This session may be vulnerable to \"store now, decrypt later\" attacks.\n" +
		"** The server may need to be upgraded. See https://openssh.com/pq.html\n"

	cases := []struct {
		name string
		raw  string
	}{
		{"banner before", pqBanner + remoteHomeMarkerStart + "/root" + remoteHomeMarkerEnd},
		{"banner after", remoteHomeMarkerStart + "/root" + remoteHomeMarkerEnd + "\n" + pqBanner},
		{"banner both sides", pqBanner + remoteHomeMarkerStart + "/root" + remoteHomeMarkerEnd + "\n" + pqBanner},
		{"clean", remoteHomeMarkerStart + "/root" + remoteHomeMarkerEnd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRemoteHome(tc.raw)
			if err != nil {
				t.Fatalf("parseRemoteHome(%q) returned error: %v", tc.raw, err)
			}
			if got != "/root" {
				t.Fatalf("parseRemoteHome(%q) = %q, want %q", tc.raw, got, "/root")
			}
		})
	}
}

// TestParseRemoteHomeRejectsInvalid pins the loud-failure contract: anything
// that is not a single absolute POSIX path between the sentinels must error
// instead of silently producing a corrupt remote path.
func TestParseRemoteHomeRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"no markers", "/root"},
		{"only start marker", remoteHomeMarkerStart + "/root"},
		{"only end marker", "/root" + remoteHomeMarkerEnd},
		{"empty home", remoteHomeMarkerStart + remoteHomeMarkerEnd},
		{"whitespace home", remoteHomeMarkerStart + "   " + remoteHomeMarkerEnd},
		{"relative path", remoteHomeMarkerStart + "relative/dir" + remoteHomeMarkerEnd},
		{"multiline home", remoteHomeMarkerStart + "/root\n/evil" + remoteHomeMarkerEnd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := parseRemoteHome(tc.raw); err == nil {
				t.Fatalf("parseRemoteHome(%q) = %q, want error", tc.raw, got)
			}
		})
	}
}

// TestRemoteHomeProbeCmdEmbedsSentinelsAndReadsHome pins the probe command so
// the sentinels wrap exactly the $HOME value and parseRemoteHome can extract
// it. The %s must sit between the two markers.
func TestRemoteHomeProbeCmdEmbedsSentinelsAndReadsHome(t *testing.T) {
	cmd := remoteHomeProbeCmd()
	if !strings.Contains(cmd, `"$HOME"`) {
		t.Fatalf("probe cmd must read $HOME: %q", cmd)
	}
	if !strings.Contains(cmd, remoteHomeMarkerStart+"%s"+remoteHomeMarkerEnd) {
		t.Fatalf("probe cmd must place %%s between the markers: %q", cmd)
	}
}

// TestSSHNoForwardArgsSuppressesBannerAndForwarding pins the shared ssh argv
// builder used by every cc-clip ssh call. LogLevel=ERROR suppresses the
// post-quantum banner (#80) at the source, ClearAllForwardings=yes keeps a
// user's global RemoteForward from triggering, `--` terminates option parsing
// before the host so a dash-leading host can never be read as a flag, and the
// remote command is wrapped in /bin/sh -c so a non-POSIX login shell (fish)
// never parses sh syntax itself.
func TestSSHNoForwardArgsSuppressesBannerAndForwarding(t *testing.T) {
	got := sshNoForwardArgs("myserver", "echo hi")
	want := []string{
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"--", "myserver", shim.WrapRemoteShell("echo hi"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sshNoForwardArgs(\"myserver\", \"echo hi\") = %#v, want %#v", got, want)
	}
}
