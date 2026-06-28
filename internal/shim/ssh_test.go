package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestSSHHostArgsInsertOptionSeparatorBeforeHost(t *testing.T) {
	args := sshHostArgs([]string{"-o", "ClearAllForwardings=yes"}, "-oProxyCommand=evil", "uname -sm")
	want := []string{"-o", "ClearAllForwardings=yes", "--", "-oProxyCommand=evil", "uname -sm"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ssh args mismatch\n got: %q\nwant: %q", args, want)
	}
}

func TestSCPHostArgsInsertOptionSeparatorBeforePaths(t *testing.T) {
	args := scpUploadArgs([]string{"-o", "ClearAllForwardings=yes"}, "-local", "-oProxyCommand=evil", "/remote")
	want := []string{"-o", "ClearAllForwardings=yes", "--", "-local", "-oProxyCommand=evil:/remote"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("scp args mismatch\n got: %q\nwant: %q", args, want)
	}
}

func TestWrapRemoteShell(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "simple command",
			cmd:  "uname -sm",
			want: `/bin/sh -c 'uname -sm'`,
		},
		{
			name: "embedded single quotes use close-escape-reopen idiom",
			cmd:  "echo 'hi'",
			want: `/bin/sh -c 'echo '\''hi'\'''`,
		},
		{
			// sh syntax that a fish login shell mis-parses (exit 127) must end up
			// fully inside the single-quoted token, never bare on the command line.
			name: "posix sh script is fully quoted",
			cmd:  "set -e\n[ -L \"$HOME/x\" ] || exit 0",
			want: "/bin/sh -c 'set -e\n[ -L \"$HOME/x\" ] || exit 0'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WrapRemoteShell(tc.cmd); got != tc.want {
				t.Errorf("WrapRemoteShell(%q)\n got: %q\nwant: %q", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestSSHArgsWrapsRemoteCommandInPosixShell(t *testing.T) {
	// The command sshd forwards to the login shell must be a single
	// `/bin/sh -c '...'` token, so a non-POSIX login shell (fish) never parses
	// sh syntax itself. Regression guard for fish-login-shell connect failures.
	s := &SSHSession{host: "myhost", controlPath: "/tmp/cc-clip-ssh-test"}
	cmd := "set -e\nmkdir -p ~/.cache/cc-clip"
	args := s.sshArgs(cmd)

	last := args[len(args)-1]
	if want := WrapRemoteShell(cmd); last != want {
		t.Fatalf("sshArgs last element\n got: %q\nwant: %q", last, want)
	}
	if !strings.HasPrefix(last, "/bin/sh -c '") {
		t.Errorf("remote command not wrapped in /bin/sh -c: %q", last)
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

func TestSetRemoteClaudeHooksEnabledTogglesMarker(t *testing.T) {
	home := t.TempDir()
	s := &localSession{home: home}
	marker := filepath.Join(home, ".cache", "cc-clip", "no-hooks")

	if err := SetRemoteClaudeHooksEnabled(s, false); err != nil {
		t.Fatalf("disable hooks failed: %v", err)
	}
	if info, err := os.Stat(marker); err != nil {
		t.Fatalf("expected no-hooks marker: %v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		got := info.Mode().Perm()
		t.Fatalf("marker mode = %o, want 0600", got)
	}

	if err := SetRemoteClaudeHooksEnabled(s, true); err != nil {
		t.Fatalf("enable hooks failed: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker removed, got err=%v", err)
	}
}

func TestUninstallRemoteClaudeWrapperIfPresentSkipsNonWrapper(t *testing.T) {
	home := t.TempDir()
	s := &localSession{home: home}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\necho real\n"), 0755); err != nil {
		t.Fatalf("write real claude: %v", err)
	}

	removed, err := UninstallRemoteClaudeWrapperIfPresent(s)
	if err != nil {
		t.Fatalf("UninstallRemoteClaudeWrapperIfPresent returned error: %v", err)
	}
	if removed {
		t.Fatal("non-wrapper claude should not be removed")
	}
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read claude: %v", err)
	}
	if !strings.Contains(string(data), "echo real") {
		t.Fatalf("real claude was modified:\n%s", data)
	}
}

func TestUninstallRemoteClaudeWrapperIfPresentRestoresSidecar(t *testing.T) {
	home := t.TempDir()
	s := &localSession{home: home}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	claudePath := filepath.Join(binDir, "claude")
	sidecarPath := filepath.Join(binDir, "claude.cc-clip-real")
	if err := os.WriteFile(claudePath, []byte("# cc-clip claude wrapper\n"), 0755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	if err := os.WriteFile(sidecarPath, []byte("#!/bin/sh\necho restored\n"), 0755); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	removed, err := UninstallRemoteClaudeWrapperIfPresent(s)
	if err != nil {
		t.Fatalf("UninstallRemoteClaudeWrapperIfPresent returned error: %v", err)
	}
	if !removed {
		t.Fatal("cc-clip wrapper should be removed")
	}
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read restored claude: %v", err)
	}
	if !strings.Contains(string(data), "echo restored") {
		t.Fatalf("sidecar was not restored:\n%s", data)
	}
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Fatalf("sidecar should be consumed, got err=%v", err)
	}
}

func TestCodexNotifyManagedBlockUsesConfigArray(t *testing.T) {
	block := codexNotifyManagedBlock("start", "end", 18339)
	if !strings.Contains(block, `notify = ["cc-clip", "plugin", "run", "codex-notify"]`) {
		t.Fatalf("expected runner notify array config, got %q", block)
	}
	if strings.Contains(block, "[notify]") {
		t.Fatalf("unexpected legacy [notify] table in %q", block)
	}
	if strings.Contains(block, "--from-codex-stdin") {
		t.Fatalf("default-port managed block must use the runner form, not the legacy --from-codex-stdin form: %q", block)
	}
}

func TestCodexNotifyManagedBlockNonDefaultPort(t *testing.T) {
	block := codexNotifyManagedBlock("start", "end", 9999)
	if !strings.Contains(block, `notify = ["env", "CC_CLIP_PORT=9999", "cc-clip", "plugin", "run", "codex-notify"]`) {
		t.Fatalf("expected runner notify array with CC_CLIP_PORT=9999 for non-default port, got %q", block)
	}
	if strings.Contains(block, "--from-codex-stdin") {
		t.Fatalf("non-default-port managed block must use the runner form: %q", block)
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
	if !strings.Contains(config, `notify = ["env", "CC_CLIP_PORT=9999", "cc-clip", "plugin", "run", "codex-notify"]`) {
		t.Fatalf("config missing managed runner notify block: %q", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigMigratesLegacyStdinBlock asserts forward
// migration: a config carrying the legacy `--from-codex-stdin` managed block is
// rebuilt into exactly one runner-form managed block (no append, no duplicate,
// legacy command gone), preserving surrounding user content.
func TestEnsureRemoteCodexNotifyConfigMigratesLegacyStdinBlock(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	const markerStart = "# >>> cc-clip notify (do not edit) >>>"
	const markerEnd = "# <<< cc-clip notify (do not edit) <<<"
	legacyBlock := markerStart + "\n" +
		`notify = ["cc-clip", "notify", "--trusted", "--from-codex-stdin"]` + "\n" +
		markerEnd
	writeTestCodexConfig(t, s.home, "model = \"gpt-5\"\n"+legacyBlock+"\n[tui.foo]\n\"gpt-5\" = 4\n")

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if strings.Contains(config, "--from-codex-stdin") {
		t.Fatalf("legacy --from-codex-stdin block must be removed after migration:\n%s", config)
	}
	if !strings.Contains(config, `notify = ["cc-clip", "plugin", "run", "codex-notify"]`) {
		t.Fatalf("migrated config must contain the runner notify block:\n%s", config)
	}
	if got := strings.Count(config, markerStart); got != 1 {
		t.Fatalf("expected exactly one managed block after migration, got %d:\n%s", got, config)
	}
	if !strings.Contains(config, "[tui.foo]") || !strings.Contains(config, `model = "gpt-5"`) {
		t.Fatalf("user content must be preserved after migration:\n%s", config)
	}
}

func TestEnsureRemoteCodexNotifyConfigRefusesUserNotify(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, `notify = ["custom", "notify"]`)

	err := EnsureRemoteCodexNotifyConfig(s, 18339)
	if err == nil {
		t.Fatal("EnsureRemoteCodexNotifyConfig should refuse an existing user notify setting")
	}
	// The refusal reason is written to the injection script's stderr then
	// exit 7. RemoteExecutor.Exec captures only stdout, so the reason must be
	// folded to stdout (2>&1) on the refusal path and surfaced in the wrapped
	// error. Otherwise the user sees only an opaque "exit status 7".
	if !strings.Contains(err.Error(), "existing top-level notify setting found") {
		t.Fatalf("error must carry the refusal reason, got: %v", err)
	}
}

// TestEnsureRemoteCodexNotifyConfigAllowsAgentSectionNotify asserts that a
// notify key living inside a sub-table such as [agents.X] does NOT trigger
// the top-level notify guard. Codex supports per-agent notify overrides;
// cc-clip's top-level injection should not be blocked by them.
//
// Regression: prior to this test the injection script used
// `grep -Eq '^[[:space:]]*notify[[:space:]]*='` which matched ANY notify
// line regardless of section context, causing `cc-clip connect --codex` to
// fail with exit status 7 whenever the user (or a previous tool) had set
// an agent-level notify.
func TestEnsureRemoteCodexNotifyConfigAllowsAgentSectionNotify(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, `model = "gpt-5"

[agents.docs_researcher]
description = "Docs researcher"
notify = ["other", "tool"]
`)

	if err := EnsureRemoteCodexNotifyConfig(s, 9999); err != nil {
		t.Fatalf("agent-level notify must not block top-level injection: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if !strings.Contains(config, "CC_CLIP_PORT=9999") {
		t.Fatalf("managed block missing after agent-section notify present: %q", config)
	}
	if !strings.Contains(config, `[agents.docs_researcher]`) {
		t.Fatalf("agent section must be preserved: %q", config)
	}
	if !strings.Contains(config, `notify = ["other", "tool"]`) {
		t.Fatalf("agent-level notify line must be preserved verbatim: %q", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigAllowsIndentedAgentSectionNotify
// covers the TOML 1.0.0 corner where a section header has leading
// whitespace. Per the spec, indentation is whitespace and ignored, so
// "  [agents.X]" remains a valid section header and any "  notify ="
// inside it must NOT be treated as a top-level notify.
//
// Regression: prior to v0.7.7 the awk used /^\[/ which only matched
// section headers at column 0, so indented headers were silently
// skipped and the notify line below them was mis-classified as
// top-level — falsely tripping exit 7.
func TestEnsureRemoteCodexNotifyConfigAllowsIndentedAgentSectionNotify(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "model = \"gpt-5\"\n"+
		"\n"+
		"  [agents.docs_researcher]\n"+
		"  description = \"Docs researcher\"\n"+
		"  notify = [\"other\", \"tool\"]\n")

	if err := EnsureRemoteCodexNotifyConfig(s, 9999); err != nil {
		t.Fatalf("indented agent-level notify must not block top-level injection: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if !strings.Contains(config, "CC_CLIP_PORT=9999") {
		t.Fatalf("managed block missing after indented agent-section notify present: %q", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigStillRefusesTopLevelNotifyAboveSection
// guards the other half of section-aware detection: a top-level notify
// that appears BEFORE any section header must still trigger refusal. This
// pins #67's original contract so it cannot be silently regressed.
func TestEnsureRemoteCodexNotifyConfigStillRefusesTopLevelNotifyAboveSection(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, `model = "gpt-5"
notify = ["users", "thing"]

[features]
foo = true
`)

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err == nil {
		t.Fatal("top-level notify (above any section) must still be refused")
	}
}

// TestEnsureRemoteCodexNotifyConfigRefusesQuotedTopLevelNotify guards the
// TOML key-equivalence corner: TOML treats notify, "notify", and 'notify'
// as the SAME bare key. A quoted top-level notify must therefore still
// trip the duplicate-key guard; otherwise cc-clip injects a second top-level
// notify and Codex rejects the config as a duplicate key.
//
// Regression: prior to this fix the awk guard matched only the bare key
// (/^[[:space:]]*notify[[:space:]]*=/), so `"notify" = [...]` slipped past
// and a duplicate was injected.
func TestEnsureRemoteCodexNotifyConfigRefusesQuotedTopLevelNotify(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name:    "double_quoted",
			content: "\"notify\" = [\"custom\", \"notify\"]\n\n[other]\nfoo = true\n",
		},
		{
			name:    "single_quoted",
			content: "'notify' = [\"custom\", \"notify\"]\n\n[other]\nfoo = true\n",
		},
		{
			name:    "double_quoted_with_leading_space",
			content: "  \"notify\" = [\"custom\"]\n\n[other]\nfoo = true\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &localSession{home: t.TempDir()}
			writeTestCodexConfig(t, s.home, tc.content)

			if err := EnsureRemoteCodexNotifyConfig(s, 18339); err == nil {
				t.Fatalf("quoted top-level notify (%s) must be refused to avoid a duplicate key", tc.name)
			}

			// The original config must be left untouched (no managed block injected).
			config := readTestCodexConfig(t, s.home)
			if strings.Contains(config, "# >>> cc-clip notify (do not edit) >>>") {
				t.Fatalf("managed block must NOT be injected when a quoted top-level notify already exists, got:\n%s", config)
			}
		})
	}
}

// TestEnsureRemoteCodexNotifyConfigAllowsQuotedAgentSectionNotify verifies
// the section-aware half still holds for quoted keys: a quoted notify inside
// a sub-table must NOT block top-level injection.
func TestEnsureRemoteCodexNotifyConfigAllowsQuotedAgentSectionNotify(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "model = \"gpt-5\"\n"+
		"\n"+
		"[agents.docs_researcher]\n"+
		"\"notify\" = [\"other\", \"tool\"]\n")

	if err := EnsureRemoteCodexNotifyConfig(s, 9999); err != nil {
		t.Fatalf("quoted agent-level notify must not block top-level injection: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if !strings.Contains(config, "CC_CLIP_PORT=9999") {
		t.Fatalf("managed block missing after quoted agent-section notify present: %q", config)
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

// TestEnsureRemoteCodexNotifyConfigPrependsBeforeSection is the primary
// regression test for the F1 (section-scoping) bug.
func TestEnsureRemoteCodexNotifyConfigPrependsBeforeSection(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, `model = "gpt-5"

[tui.model_availability_nux]
"gpt-5" = 4
`)

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	notifyIdx := strings.Index(config, "notify =")
	sectionIdx := strings.Index(config, "[tui.model_availability_nux]")
	if notifyIdx < 0 || sectionIdx < 0 {
		t.Fatalf("expected both notify and section in config: %q", config)
	}
	if notifyIdx > sectionIdx {
		t.Fatalf("notify must appear before [tui.model_availability_nux] to stay at TOML top level, got:\n%s", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigFirstLineSection covers the corner case
// where the very first line is a [section] header.
func TestEnsureRemoteCodexNotifyConfigFirstLineSection(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "[tui.model_availability_nux]\n\"gpt-5\" = 4\n")

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	notifyIdx := strings.Index(config, "notify =")
	sectionIdx := strings.Index(config, "[tui.model_availability_nux]")
	if notifyIdx < 0 || sectionIdx < 0 || notifyIdx > sectionIdx {
		t.Fatalf("notify must precede the first-line section, got:\n%s", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigNoTrailingNewline ensures the rewrite
// does not concatenate the managed block with the last line.
func TestEnsureRemoteCodexNotifyConfigNoTrailingNewline(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, `model = "gpt-5"`)

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if strings.Contains(config, `gpt-5"# >>>`) || strings.Contains(config, `gpt-5"#>>>`) {
		t.Fatalf("managed block must not concatenate with trailing line, got:\n%s", config)
	}
	if !strings.HasSuffix(config, "\n") {
		t.Fatalf("rewritten config must end with newline, got: %q", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigIdempotentReinjection asserts running
// the injection twice yields a byte-identical result.
func TestEnsureRemoteCodexNotifyConfigIdempotentReinjection(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "model = \"gpt-5\"\n\n[tui.foo]\n\"gpt-5\" = 4\n")

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("first injection failed: %v", err)
	}
	first := readTestCodexConfig(t, s.home)

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("second injection failed: %v", err)
	}
	second := readTestCodexConfig(t, s.home)

	if first != second {
		t.Fatalf("re-injection must be byte-identical.\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Count(second, "# >>> cc-clip notify (do not edit) >>>") != 1 {
		t.Fatalf("expected exactly one managed block, got:\n%s", second)
	}
}

// TestEnsureRemoteCodexNotifyConfigEmptyFile covers an empty existing
// config — no extra blank lines should pad the managed block.
func TestEnsureRemoteCodexNotifyConfigEmptyFile(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "")

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if !strings.Contains(config, "# >>> cc-clip notify (do not edit) >>>") {
		t.Fatalf("managed block missing: %q", config)
	}
	if strings.Contains(config, "\n\n\n") {
		t.Fatalf("empty file should not produce triple newlines, got: %q", config)
	}
}

// TestEnsureRemoteCodexNotifyConfigFileDoesNotExist covers the case
// where ~/.codex/ itself does not exist yet.
func TestEnsureRemoteCodexNotifyConfigFileDoesNotExist(t *testing.T) {
	s := &localSession{home: t.TempDir()}

	if err := EnsureRemoteCodexNotifyConfig(s, 18339); err != nil {
		t.Fatalf("EnsureRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if !strings.Contains(config, "notify =") {
		t.Fatalf("managed block missing after first-run write: %q", config)
	}
}

// TestStripRemoteCodexNotifyConfigRemovesManagedBlock covers the
// uninstall companion: managed block must be removed, user content
// preserved.
func TestStripRemoteCodexNotifyConfigRemovesManagedBlock(t *testing.T) {
	s := &localSession{home: t.TempDir()}
	writeTestCodexConfig(t, s.home, "model = \"gpt-5\"\n"+
		codexNotifyManagedBlock("# >>> cc-clip notify (do not edit) >>>", "# <<< cc-clip notify (do not edit) <<<", 18339)+
		"\n[tui.foo]\n\"gpt-5\" = 4\n")

	if err := StripRemoteCodexNotifyConfig(s); err != nil {
		t.Fatalf("StripRemoteCodexNotifyConfig returned error: %v", err)
	}

	config := readTestCodexConfig(t, s.home)
	if strings.Contains(config, "cc-clip notify") {
		t.Fatalf("managed block must be fully removed, got:\n%s", config)
	}
	if !strings.Contains(config, "[tui.foo]") || !strings.Contains(config, `model = "gpt-5"`) {
		t.Fatalf("user content must be preserved, got:\n%s", config)
	}
}

// TestStripRemoteCodexNotifyConfigNoOpWhenAbsent verifies that strip on
// a config without a managed block (or missing file) is a no-op.
func TestStripRemoteCodexNotifyConfigNoOpWhenAbsent(t *testing.T) {
	s := &localSession{home: t.TempDir()}

	original := `model = "gpt-5"` + "\n[tui.foo]\n\"gpt-5\" = 4\n"
	writeTestCodexConfig(t, s.home, original)
	if err := StripRemoteCodexNotifyConfig(s); err != nil {
		t.Fatalf("strip on no-block file failed: %v", err)
	}
	if got := readTestCodexConfig(t, s.home); got != original {
		t.Fatalf("strip must not modify file without managed block.\nwant: %q\ngot:  %q", original, got)
	}

	s2 := &localSession{home: t.TempDir()}
	if err := StripRemoteCodexNotifyConfig(s2); err != nil {
		t.Fatalf("strip on missing file must be no-op, got: %v", err)
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
