package shim

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupFakeHome creates a t.TempDir() with .local/bin/ subdir and returns
// the bin dir path. Tests use this as a fake remote $HOME root.
func setupFakeHome(t *testing.T) (home, binDir string) {
	t.Helper()
	home = t.TempDir()
	binDir = filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("setup binDir: %v", err)
	}
	return home, binDir
}

func TestClassifyClaudeBin_None(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not reliably available on Windows runner")
	}
	home, _ := setupFakeHome(t)
	s := &localSession{home: home}
	kind, err := classifyClaudeBin(s)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if kind != "none" {
		t.Fatalf("got %q, want none", kind)
	}
}

func TestClassifyClaudeBin_RegularNonWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not reliably available on Windows runner")
	}
	home, binDir := setupFakeHome(t)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("\x7fELF... fake"), 0755); err != nil {
		t.Fatal(err)
	}
	s := &localSession{home: home}
	kind, err := classifyClaudeBin(s)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "regular" {
		t.Fatalf("got %q, want regular", kind)
	}
}

func TestClassifyClaudeBin_CcWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not reliably available on Windows runner")
	}
	home, binDir := setupFakeHome(t)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(ClaudeWrapperScript(18339)), 0755); err != nil {
		t.Fatal(err)
	}
	s := &localSession{home: home}
	kind, err := classifyClaudeBin(s)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "cc_wrapper" {
		t.Fatalf("got %q, want cc_wrapper", kind)
	}
}

func TestClassifyClaudeBin_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	home, binDir := setupFakeHome(t)
	target := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.132")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("\x7fELF... real binary content"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}
	s := &localSession{home: home}
	kind, err := classifyClaudeBin(s)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "symlink" {
		t.Fatalf("got %q, want symlink", kind)
	}
}

func TestInstall_RegularFileOrigin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	originalContent := []byte("\x7fELF... pretend this is the real 250MB claude binary")
	if err := os.WriteFile(filepath.Join(binDir, "claude"), originalContent, 0755); err != nil {
		t.Fatal(err)
	}
	s := &localSession{home: home}

	if err := InstallRemoteClaudeWrapper(s, 18339); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Sidecar must hold the original (verbatim).
	sidecar, err := os.ReadFile(filepath.Join(binDir, "claude.cc-clip-real"))
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	if string(sidecar) != string(originalContent) {
		t.Fatal("sidecar does not contain original content (mv may have leaked or content was rewritten)")
	}

	// claude must now be the wrapper.
	data, err := os.ReadFile(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# cc-clip claude wrapper") {
		t.Fatal("claude is not the cc-clip wrapper after install")
	}
	// Port-substitution assertion (per T6 review note): wrapper must reference
	// the port we passed to InstallRemoteClaudeWrapper.
	if !strings.Contains(string(data), "18339") {
		t.Fatal("installed wrapper does not contain expected port 18339")
	}

	// claude must be a regular file (not a symlink).
	info, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("claude should be a regular file after install")
	}
}

func TestInstall_NoPriorInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	s := &localSession{home: home}

	if err := InstallRemoteClaudeWrapper(s, 18339); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Wrapper should now exist as a regular file at ~/.local/bin/claude.
	info, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatalf("claude not installed: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("claude should be a regular file, got symlink")
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Fatal("claude should be executable")
	}

	// No sidecar should have been created (no origin to displace).
	if _, err := os.Lstat(filepath.Join(binDir, "claude.cc-clip-real")); !os.IsNotExist(err) {
		t.Fatalf("sidecar should not exist on first install of 'none' case, got: %v", err)
	}

	// Content must be the wrapper script.
	data, err := os.ReadFile(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# cc-clip claude wrapper") {
		t.Fatal("installed file is not the cc-clip wrapper")
	}
}

func TestInstall_ReinstallOverCcWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)

	// Existing cc-clip wrapper at claude (port 18339).
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(ClaudeWrapperScript(18339)), 0755); err != nil {
		t.Fatal(err)
	}
	// Existing sidecar from prior install.
	sidecarContent := []byte("PRIOR-SIDECAR-CONTENT")
	if err := os.WriteFile(filepath.Join(binDir, "claude.cc-clip-real"), sidecarContent, 0755); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	if err := InstallRemoteClaudeWrapper(s, 19999); err != nil {
		t.Fatalf("re-install: %v", err)
	}

	// Wrapper must be updated (port baked in is 19999 now).
	data, err := os.ReadFile(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "19999") {
		t.Fatal("wrapper port was not updated")
	}
	if strings.Contains(string(data), "18339") {
		t.Fatal("wrapper still contains old port")
	}

	// Sidecar must be untouched (we don't displace re-install over our own wrapper).
	sidecarPost, err := os.ReadFile(filepath.Join(binDir, "claude.cc-clip-real"))
	if err != nil {
		t.Fatalf("sidecar missing after re-install: %v", err)
	}
	if string(sidecarPost) != string(sidecarContent) {
		t.Fatal("sidecar was modified during re-install (must be untouched)")
	}
}

func TestInstall_SidecarCollision_RegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)

	// Native installer layout (symlink origin).
	versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versionsDir, "2.1.132")
	if err := os.WriteFile(target, []byte("real binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}

	// Stale sidecar from a prior half-install.
	sidecar := filepath.Join(binDir, "claude.cc-clip-real")
	if err := os.WriteFile(sidecar, []byte("STALE"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	err := InstallRemoteClaudeWrapper(s, 18339)
	if err == nil {
		t.Fatal("expected install to refuse with collision; got nil")
	}
	if !strings.Contains(err.Error(), "claude.cc-clip-real already exists") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Origin must be untouched.
	info, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("origin symlink was disturbed despite collision refusal")
	}
}

func TestInstall_SidecarCollision_Directory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)

	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("regular"), 0755); err != nil {
		t.Fatal(err)
	}
	// Sidecar PATH is a directory — would have been the silent-corruption footgun.
	sidecarDir := filepath.Join(binDir, "claude.cc-clip-real")
	if err := os.Mkdir(sidecarDir, 0755); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	err := InstallRemoteClaudeWrapper(s, 18339)
	if err == nil {
		t.Fatal("expected install to refuse on directory at sidecar path")
	}
	// CRITICAL: origin must NOT have been moved into the directory.
	if _, statErr := os.Stat(filepath.Join(sidecarDir, "claude")); statErr == nil {
		t.Fatal("FOOTGUN: claude was moved into the directory; collision guard failed")
	}
	// Origin must still be at the original location.
	if _, err := os.Lstat(filepath.Join(binDir, "claude")); err != nil {
		t.Fatal("origin claude is missing; collision guard failed to short-circuit")
	}
}

func TestInstall_SymlinkOrigin_NativeInstallerLayout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	home, binDir := setupFakeHome(t)

	// Build Native Installer layout:
	//   ~/.local/bin/claude -> ~/.local/share/claude/versions/2.1.132
	//   ~/.local/share/claude/versions/2.1.132 = real binary (5MB random)
	versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versionsDir, "2.1.132")
	realBinary := make([]byte, 5*1024*1024)
	for i := range realBinary {
		realBinary[i] = byte(i % 251) // pseudo-random; large enough to be distinguishable
	}
	if err := os.WriteFile(target, realBinary, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}

	// Snapshot the real binary content before install.
	pre, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	if err := InstallRemoteClaudeWrapper(s, 18339); err != nil {
		t.Fatalf("install: %v", err)
	}

	// CRITICAL: real binary in versions store must be byte-identical to before.
	// This is the issue #55 regression assertion — the original v0.7.0 bug had
	// the wrapper written THROUGH the symlink to this very file.
	post, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("real binary disappeared: %v", err)
	}
	if string(pre) != string(post) {
		t.Fatal("REGRESSION: real claude binary was modified during install (issue #55 root bug)")
	}

	// Sidecar must be the symlink itself, still pointing at versions/2.1.132.
	sidecar := filepath.Join(binDir, "claude.cc-clip-real")
	info, err := os.Lstat(sidecar)
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("sidecar should be a symlink (origin was a symlink)")
	}
	resolved, err := os.Readlink(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target {
		t.Fatalf("sidecar target: got %q, want %q", resolved, target)
	}

	// Wrapper must be a regular file at claude path.
	wrapperInfo, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if wrapperInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("claude should be a regular file after install (the wrapper)")
	}
}

// capturingSession records the install command(s) so we can assert on the
// generated bash without executing it. Exec runs normally (pre-flight probe);
// ExecWithStdin captures the script body and then delegates.
type capturingSession struct {
	*localSession
	lastStdinCmd string
}

func (c *capturingSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
	c.lastStdinCmd = cmd
	return c.localSession.ExecWithStdin(cmd, stdin)
}

// TestInstall_SidecarTrapRestoresOnAbort asserts the with-sidecar install
// command tracks a `committed` flag and that its EXIT trap restores the
// sidecar back to ~/.local/bin/claude when the install was NOT committed.
//
// Regression: the trap previously only did `rm -f "$tmp"`. An external abort
// (SSH disconnect / SIGKILL) AFTER the staging mv (claude -> claude.cc-clip-real)
// but BEFORE the commit mv (tmp -> claude) left ~/.local/bin/claude missing,
// breaking the user's claude even though the docstring promised rollback.
func TestInstall_SidecarTrapRestoresOnAbort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("ORIGINAL"), 0755); err != nil {
		t.Fatal(err)
	}

	cap := &capturingSession{localSession: &localSession{home: home}}
	if err := InstallRemoteClaudeWrapper(cap, 18339); err != nil {
		t.Fatalf("install: %v", err)
	}

	cmd := cap.lastStdinCmd
	// The script must introduce a committed marker and set it after the
	// commit mv succeeds.
	if !strings.Contains(cmd, "committed") {
		t.Fatal("with-sidecar install script must track a `committed` flag for trap-based rollback")
	}
	// The EXIT trap must restore the sidecar when not committed (not merely rm the tmp).
	if !strings.Contains(cmd, "claude.cc-clip-real") || !strings.Contains(cmd, "trap") {
		t.Fatal("with-sidecar install script must restore the sidecar from the EXIT trap")
	}
	// Specifically, the trap body must reference the sidecar restore mv so an
	// abort between the two mvs cannot leave claude missing.
	if !strings.Contains(cmd, `mv "$HOME/.local/bin/claude.cc-clip-real" "$HOME/.local/bin/claude"`) {
		t.Fatal("EXIT trap must restore claude from the sidecar on abort")
	}
}

// TestInstall_SidecarAbortBeforeCommitRestoresClaude simulates an abort that
// happens AFTER the staging mv but BEFORE the commit mv, by truncating the
// generated script at the staging point and running the EXIT trap. After the
// truncated run, ~/.local/bin/claude must be restored (not missing).
func TestInstall_SidecarAbortBeforeCommitRestoresClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("ORIGINAL-CLAUDE"), 0755); err != nil {
		t.Fatal(err)
	}

	cap := &capturingSession{localSession: &localSession{home: home}}
	if err := InstallRemoteClaudeWrapper(cap, 18339); err != nil {
		t.Fatalf("install (to capture script): %v", err)
	}

	// Reset filesystem to the pre-install state for the abort simulation.
	if err := os.RemoveAll(binDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("ORIGINAL-CLAUDE"), 0755); err != nil {
		t.Fatal(err)
	}

	// Truncate the captured script right after the staging mv so the commit
	// mv never runs, then `exit 1` to fire the EXIT trap — modeling an abort
	// between the two mvs.
	full := cap.lastStdinCmd
	marker := `mv "$HOME/.local/bin/claude" "$HOME/.local/bin/claude.cc-clip-real"`
	idx := strings.Index(full, marker)
	if idx < 0 {
		t.Fatalf("staging mv not found in generated script:\n%s", full)
	}
	truncated := full[:idx+len(marker)] + "\nexit 1\n"

	c := exec.Command("bash", "-c", truncated)
	c.Env = append(os.Environ(), "HOME="+home, "PATH=/usr/bin:/bin")
	c.Stdin = strings.NewReader("WRAPPER-CONTENT")
	_ = c.Run() // exit 1 expected

	// CRITICAL: claude must be restored by the trap, not left missing.
	data, err := os.ReadFile(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatalf("claude missing after abort between mvs — trap did not restore: %v", err)
	}
	if string(data) != "ORIGINAL-CLAUDE" {
		t.Fatalf("claude content not restored on abort, got %q", string(data))
	}
}

// rollbackInjectingSession wraps localSession and rewrites the install
// command to force the final mv to fail, simulating a filesystem error
// after the staging mv has already moved origin to the sidecar.
type rollbackInjectingSession struct {
	*localSession
	binDir   string
	injected bool
}

func (r *rollbackInjectingSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
	if !r.injected && strings.Contains(cmd, "claude.cc-clip-real") {
		r.injected = true
		// Force the commit mv to fail so the EXIT trap's rollback branch
		// (restore sidecar -> claude) is exercised. Under `set -e` the
		// failing command aborts the script and fires the trap, exactly
		// like an external abort between the staging and commit mvs.
		cmd = strings.Replace(cmd,
			`mv "$tmp" "$HOME/.local/bin/claude"`,
			`false`, 1)
	}
	return r.localSession.ExecWithStdin(cmd, stdin)
}

func TestInstall_RollbackOnFinalMvFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	originalContent := []byte("ORIGINAL-CLAUDE-BINARY-MUST-BE-RESTORED")
	if err := os.WriteFile(filepath.Join(binDir, "claude"), originalContent, 0755); err != nil {
		t.Fatal(err)
	}

	s := &rollbackInjectingSession{localSession: &localSession{home: home}, binDir: binDir}
	err := InstallRemoteClaudeWrapper(s, 18339)
	if err == nil {
		t.Fatal("expected install to fail (we injected final-mv failure)")
	}

	// Origin must be restored at ~/.local/bin/claude.
	restored, err := os.ReadFile(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatalf("origin not restored: %v", err)
	}
	if string(restored) != string(originalContent) {
		t.Fatal("origin content corrupted; rollback failed")
	}

	// Sidecar must NOT exist after rollback.
	if _, err := os.Lstat(filepath.Join(binDir, "claude.cc-clip-real")); !os.IsNotExist(err) {
		t.Fatal("sidecar lingered after rollback")
	}
}

func TestInstall_MktempDoesNotFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("regular origin"), 0755); err != nil {
		t.Fatal(err)
	}

	// Plant a sensitive file outside binDir; install must not touch it.
	// mktemp uses XXXXXX (random suffix), so collision probability is ~1/(62^6).
	// This test confirms install succeeds and the sensitive file is unchanged,
	// verifying mktemp is in use rather than a predictable name like $$.
	sensitive := filepath.Join(home, "sensitive-file.txt")
	if err := os.WriteFile(sensitive, []byte("DO NOT TOUCH"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	if err := InstallRemoteClaudeWrapper(s, 18339); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Sensitive file must be unchanged.
	pre, _ := os.ReadFile(sensitive)
	if string(pre) != "DO NOT TOUCH" {
		t.Fatal("sensitive file was modified — mktemp may not be in use")
	}
}

// makeV070Corruption sets up the exact filesystem state issue #55 describes:
// symlink at ~/.local/bin/claude points at versions/X.Y.Z, that file is now
// a cc-clip wrapper, and ~/.local/bin/claude.cc-clip-bak holds the real binary.
func makeV070Corruption(t *testing.T, home string) {
	t.Helper()
	binDir := filepath.Join(home, ".local", "bin")
	versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versionsDir, "2.1.132")
	// versions/X.Y.Z is a cc-clip wrapper (the bug).
	if err := os.WriteFile(target, []byte(ClaudeWrapperScript(18339)), 0755); err != nil {
		t.Fatal(err)
	}
	// claude is the symlink, untouched.
	if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}
	// .cc-clip-bak holds the original 5MB ELF.
	realBinary := make([]byte, 5*1024*1024)
	for i := range realBinary {
		realBinary[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude.cc-clip-bak"), realBinary, 0755); err != nil {
		t.Fatal(err)
	}
}

// makeSymlinkToWrapperOnly builds the "target is wrapper" half of the
// v0.7.0 layout (C1+C2 hold) but leaves the .cc-clip-bak file to the caller
// — so tests can craft any combination of C3/C4/C5 failures.
func makeSymlinkToWrapperOnly(t *testing.T, home string) string {
	t.Helper()
	binDir := filepath.Join(home, ".local", "bin")
	versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versionsDir, "2.1.132")
	if err := os.WriteFile(target, []byte(ClaudeWrapperScript(18339)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(binDir, "claude.cc-clip-bak")
}

// TestDetectV070State_TableDriven exhaustively covers the three-state
// detector's decision boundary. Each row builds a specific filesystem
// shape and asserts both the state classification AND the stable diag
// token — diag tokens are part of the contract used by callers for
// log messages and (importantly) for the manual-recovery hint at N0.
func TestDetectV070State_TableDriven(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}

	cases := []struct {
		name      string
		setup     func(t *testing.T, home string)
		wantState V070State
		wantDiag  string
	}{
		{
			name:      "no_claude_at_all",
			setup:     func(t *testing.T, home string) {},
			wantState: V070NotCorrupted,
			wantDiag:  "not_corrupted_C1_not_symlink",
		},
		{
			name: "claude_is_regular_file_with_backup",
			setup: func(t *testing.T, home string) {
				binDir := filepath.Join(home, ".local", "bin")
				if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("regular"), 0755); err != nil {
					t.Fatal(err)
				}
				// Even with a .cc-clip-bak, C1 fails (not symlink).
				if err := os.WriteFile(filepath.Join(binDir, "claude.cc-clip-bak"), make([]byte, 5*1024*1024), 0755); err != nil {
					t.Fatal(err)
				}
			},
			wantState: V070NotCorrupted,
			wantDiag:  "not_corrupted_C1_not_symlink",
		},
		{
			name: "symlink_target_not_wrapper",
			setup: func(t *testing.T, home string) {
				binDir := filepath.Join(home, ".local", "bin")
				versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
				if err := os.MkdirAll(versionsDir, 0755); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(versionsDir, "2.1.132")
				if err := os.WriteFile(target, []byte("\x7fELF... real binary"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
					t.Fatal(err)
				}
			},
			wantState: V070NotCorrupted,
			wantDiag:  "not_corrupted_C2_not_wrapper",
		},
		{
			name: "canonical_v070_state_recoverable",
			setup: func(t *testing.T, home string) {
				makeV070Corruption(t, home)
			},
			wantState: V070Recoverable,
			wantDiag:  "recoverable",
		},
		{
			name: "wrapper_target_backup_missing",
			setup: func(t *testing.T, home string) {
				makeSymlinkToWrapperOnly(t, home)
				// Intentionally no .cc-clip-bak: spec scenario 12.
			},
			wantState: V070NonRecoverable,
			wantDiag:  "non_recoverable_C3_backup_missing",
		},
		{
			name: "wrapper_target_backup_too_small",
			setup: func(t *testing.T, home string) {
				bakPath := makeSymlinkToWrapperOnly(t, home)
				// Below 1 MiB threshold — cannot be a real claude binary.
				if err := os.WriteFile(bakPath, make([]byte, 512*1024), 0755); err != nil {
					t.Fatal(err)
				}
			},
			wantState: V070NonRecoverable,
			wantDiag:  "non_recoverable_C4_backup_too_small",
		},
		{
			name: "wrapper_target_backup_is_wrapper",
			setup: func(t *testing.T, home string) {
				bakPath := makeSymlinkToWrapperOnly(t, home)
				// Backup is also a wrapper. Pad to >1MB so C4 passes
				// and C5 is the deciding failure: spec scenario 13.
				bakContent := append([]byte(ClaudeWrapperScript(18339)), make([]byte, 2*1024*1024)...)
				if err := os.WriteFile(bakPath, bakContent, 0755); err != nil {
					t.Fatal(err)
				}
			},
			wantState: V070NonRecoverable,
			wantDiag:  "non_recoverable_C5_backup_is_wrapper",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := setupFakeHome(t)
			tc.setup(t, home)
			s := &localSession{home: home}
			state, diag, err := DetectV070State(s)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if state != tc.wantState {
				t.Errorf("state: got %v, want %v (diag=%q)", state, tc.wantState, diag)
			}
			if diag != tc.wantDiag {
				t.Errorf("diag: got %q, want %q", diag, tc.wantDiag)
			}
		})
	}
}

// TestDetectV070Corruption_BooleanCompat verifies the compat wrapper still
// returns true ONLY for the recoverable case. Non-recoverable states must
// collapse to false so RecoverV070Corruption's TOCTOU guard fails safe.
func TestDetectV070Corruption_BooleanCompat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, home string)
		want  bool
	}{
		{
			name:  "recoverable_yields_true",
			setup: func(t *testing.T, home string) { makeV070Corruption(t, home) },
			want:  true,
		},
		{
			name: "non_recoverable_collapses_to_false",
			setup: func(t *testing.T, home string) {
				makeSymlinkToWrapperOnly(t, home) // C3 fails — non-recoverable
			},
			want: false,
		},
		{
			name:  "not_corrupted_yields_false",
			setup: func(t *testing.T, home string) {},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := setupFakeHome(t)
			tc.setup(t, home)
			s := &localSession{home: home}
			got, err := DetectV070Corruption(s)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecoverV070_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	home, binDir := setupFakeHome(t)
	makeV070Corruption(t, home)

	target := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.132")
	bakBefore, _ := os.ReadFile(filepath.Join(binDir, "claude.cc-clip-bak"))

	s := &localSession{home: home}
	if err := RecoverV070Corruption(s); err != nil {
		t.Fatalf("recover: %v", err)
	}

	// After recovery: target file content == backup content (real binary restored).
	targetAfter, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetAfter) != string(bakBefore) {
		t.Fatal("target content does not match backup; recovery failed")
	}

	// Backup file must be gone (mv consumed it).
	if _, err := os.Lstat(filepath.Join(binDir, "claude.cc-clip-bak")); !os.IsNotExist(err) {
		t.Fatal("backup file lingered after recovery")
	}

	// Symlink must still exist and still point at target.
	info, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("claude is no longer a symlink after recovery")
	}
}

func TestRecoverV070_RefusesIfNotCorrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("regular"), 0755); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	err := RecoverV070Corruption(s)
	if err == nil {
		t.Fatal("expected recovery to refuse when no corruption is detected")
	}
}

func TestUninstall_SymlinkOrigin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	home, binDir := setupFakeHome(t)
	versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versionsDir, "2.1.132")
	realBinary := []byte("real claude binary content, ~5MB pretend")
	if err := os.WriteFile(target, realBinary, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	if err := InstallRemoteClaudeWrapper(s, 18339); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := UninstallRemoteClaudeWrapper(s); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	// claude must be a symlink again, pointing at the original target.
	info, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("claude is not a symlink after uninstall")
	}
	resolved, err := os.Readlink(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target {
		t.Fatalf("symlink target: got %q, want %q", resolved, target)
	}
	// Real binary intact (was never touched).
	pre, _ := os.ReadFile(target)
	if string(pre) != string(realBinary) {
		t.Fatal("real binary corrupted")
	}
	// Sidecar must be gone.
	if _, err := os.Lstat(filepath.Join(binDir, "claude.cc-clip-real")); !os.IsNotExist(err) {
		t.Fatal("sidecar lingered after uninstall")
	}
}

func TestUninstall_RegularFileOrigin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	original := []byte("ORIGINAL")
	if err := os.WriteFile(filepath.Join(binDir, "claude"), original, 0755); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	if err := InstallRemoteClaudeWrapper(s, 18339); err != nil {
		t.Fatal(err)
	}
	if err := UninstallRemoteClaudeWrapper(s); err != nil {
		t.Fatal(err)
	}

	// claude must be the original regular file again.
	info, err := os.Lstat(filepath.Join(binDir, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("claude is a symlink after uninstall (should be regular)")
	}
	data, _ := os.ReadFile(filepath.Join(binDir, "claude"))
	if string(data) != string(original) {
		t.Fatal("origin content not restored")
	}
}

func TestUninstall_RefusesForeignFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based install path is Linux-only")
	}
	home, binDir := setupFakeHome(t)
	foreign := []byte("#!/bin/bash\n# user's own custom claude wrapper, not ours\n")
	if err := os.WriteFile(filepath.Join(binDir, "claude"), foreign, 0755); err != nil {
		t.Fatal(err)
	}

	s := &localSession{home: home}
	err := UninstallRemoteClaudeWrapper(s)
	if err == nil {
		t.Fatal("expected uninstall to refuse foreign file")
	}
	if !strings.Contains(err.Error(), "not a cc-clip wrapper") {
		t.Fatalf("unexpected uninstall error: %v", err)
	}
	// Foreign file must be untouched.
	data, _ := os.ReadFile(filepath.Join(binDir, "claude"))
	if string(data) != string(foreign) {
		t.Fatal("foreign file was modified")
	}
}
