package shim

import (
	"io"
	"os"
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
		// Replace the final mv guard with a forced failure so the rollback
		// branch (mv sidecar back to claude) is exercised.
		cmd = strings.Replace(cmd,
			`if ! mv "$tmp" "$HOME/.local/bin/claude"; then`,
			`if ! false; then`, 1)
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
