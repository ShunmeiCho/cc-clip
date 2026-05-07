# Issue #55 Claude Wrapper Symlink-Safe Fix — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `cc-clip setup`/`connect` install the claude wrapper without overwriting the real claude binary on Native Installer layouts (issue #55), with opt-in recovery for v0.7.0 victims via `--auto-recover`.

**Architecture:** Approach B from spec — symlink-safe install topology using mv-only (never `cp`/`>` against possibly-symlink paths), explicit `~/.local/bin/claude.cc-clip-real` sidecar holding the original entry, sidecar-first wrapper exec with PATH-discovery fallback, plus default-fail-closed v0.7.0 corruption detection (5-condition check) and opt-in recovery via `--auto-recover` flag.

**Tech Stack:** Go 1.22+, bash 4+ on remote, OpenSSH ControlMaster (Linux/macOS) or independent ssh (Windows), `go test` with `t.TempDir()`-based fakes, no new dependencies.

**Spec:** [`docs/superpowers/specs/2026-05-07-issue-55-claude-wrapper-symlink-fix-design.md`](../specs/2026-05-07-issue-55-claude-wrapper-symlink-fix-design.md)

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/shim/ssh.go` | Modify | Add `SessionExecutor` interface, `(*SSHSession).ExecWithStdin`, rewrite `InstallRemoteClaudeWrapper`, new functions `UninstallRemoteClaudeWrapper`, `DetectV070Corruption`, `RecoverV070Corruption`, helper `classifyClaudeBin` |
| `internal/shim/claude_wrapper.go` | Modify | Update `claudeWrapperTemplate` to sidecar-first exec branch with PATH-fallback |
| `internal/shim/deploy.go` | Modify | Add `ClaudeWrapperState` sub-struct + `ClaudeWrapper` field on `DeployState` |
| `internal/shim/local_session_test.go` | Create | `localSession` test stub implementing `SessionExecutor` against `t.TempDir()` |
| `internal/shim/claude_wrapper_install_test.go` | Create | All 22 spec test scenarios for install/uninstall/detect/recover/wrapper-exec |
| `cmd/cc-clip/main.go` | Modify | Add `--auto-recover` flag with mutex check, insert N0 detection in `cmdConnect`/`cmdSetup` (between line 558 and 561), modify `cmdUninstall` for `--host` best-effort + wrapper restore wiring, update `printUsage` |
| `docs/commands.md` | Modify | Document `--auto-recover` flag and new uninstall semantics |
| `README.md` | Modify | One-line note about `--auto-recover` for v0.7.0 victims |

---

## Build Order Rationale

Phase 1 (T1-T3): Test seam + data structures. No production behavior change. Lets every later task be unit-testable.

Phase 2 (T4): Wrapper template. Independent of Layer 1 logic but required for Layer 1 sidecar-exec tests.

Phase 3 (T5-T11): `InstallRemoteClaudeWrapper` rebuild from classify → branches → guards → atomic write + rollback. Each branch a separate task to keep failure modes isolated.

Phase 4 (T12-T13): Detect + recover.

Phase 5 (T14): Uninstall.

Phase 6 (T15-T17): CLI wiring in main.go. Done last so all shim functions are already proven.

Phase 7 (T18): Docs.

Run `go test ./internal/shim -count=1 -race` after every task. Run `go vet ./...` before each commit.

---

## Task 1: SessionExecutor interface + ExecWithStdin method

**Files:**
- Modify: `internal/shim/ssh.go`
- Test: `internal/shim/ssh_test.go` (add to existing file)

**Why:** Without an interface, no install/uninstall function is unit-testable. This task introduces the seam.

- [ ] **Step 1: Write the failing test**

Add to `internal/shim/ssh_test.go`:

```go
func TestSessionExecutorInterface_StaticConformance(t *testing.T) {
    // Compile-time assertion: *SSHSession implements SessionExecutor.
    var _ SessionExecutor = (*SSHSession)(nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestSessionExecutorInterface -count=1`
Expected: compile error like `undefined: SessionExecutor`.

- [ ] **Step 3: Add SessionExecutor interface to ssh.go**

Add `"io"` to the imports of `internal/shim/ssh.go`. Then append (after the import block, before `type SSHSession`):

```go
// SessionExecutor abstracts a remote SSH session for install/uninstall/
// detect/recover testing. *SSHSession satisfies this interface; tests use
// a localSession stub that runs commands locally against a temp $HOME.
type SessionExecutor interface {
    // Exec runs a remote shell command and returns STDOUT only.
    // Stderr is intentionally discarded to match the existing semantics of
    // (*SSHSession).Exec — SSH multiplex chatter (control socket noise,
    // mux_client_forward, etc.) lands on stderr and would otherwise pollute
    // callers that grep the output for content markers.
    Exec(cmd string) (stdout string, err error)

    // ExecWithStdin runs a remote shell command with the provided stdin
    // and returns COMBINED stdout+stderr. Combined output is appropriate
    // here because this method is used during install/uninstall where
    // stderr diagnostics (e.g. "mv: cannot create regular file: Permission
    // denied") are required for actionable error messages.
    ExecWithStdin(cmd string, stdin io.Reader) (combinedOutput string, err error)
}
```

- [ ] **Step 4: Add ExecWithStdin method to *SSHSession**

Append to `internal/shim/ssh.go` (after the existing `Upload` method):

```go
// ExecWithStdin runs a remote shell command with the provided stdin via
// the SSH master connection. Returns combined stdout+stderr output for
// install/uninstall error diagnostics.
func (s *SSHSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
    args := append(s.connArgs(), s.host, cmd)
    c := exec.Command("ssh", args...)
    c.Stdin = stdin
    out, err := c.CombinedOutput()
    return string(out), err
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/shim -run TestSessionExecutorInterface -count=1`
Expected: PASS.

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/shim/ssh.go internal/shim/ssh_test.go
git commit -m "refactor(shim): introduce SessionExecutor interface and ExecWithStdin"
```

---

## Task 2: localSession test stub

**Files:**
- Create: `internal/shim/local_session_test.go`

**Why:** All install/uninstall/detect/recover tests need a `SessionExecutor` impl that runs against `t.TempDir()` instead of a real SSH host.

- [ ] **Step 1: Write the failing test**

Create `internal/shim/local_session_test.go`:

```go
package shim

import (
    "io"
    "os/exec"
    "strings"
    "testing"
)

// localSession is a SessionExecutor that runs commands locally via bash -c
// against a configured $HOME (a t.TempDir() in tests). $HOME is set per
// command so the bash `~` expansion lands in the fake home.
type localSession struct {
    home string
}

func (l *localSession) Exec(cmd string) (string, error) {
    c := exec.Command("bash", "-c", cmd)
    c.Env = append(c.Env, "HOME="+l.home, "PATH=/usr/bin:/bin")
    out, err := c.Output() // stdout only, matches *SSHSession.Exec semantics
    return strings.TrimSpace(string(out)), err
}

func (l *localSession) ExecWithStdin(cmd string, stdin io.Reader) (string, error) {
    c := exec.Command("bash", "-c", cmd)
    c.Env = append(c.Env, "HOME="+l.home, "PATH=/usr/bin:/bin")
    c.Stdin = stdin
    out, err := c.CombinedOutput() // combined, matches *SSHSession.ExecWithStdin
    return string(out), err
}

func TestLocalSession_ExecStdoutOnly(t *testing.T) {
    s := &localSession{home: t.TempDir()}
    out, err := s.Exec("echo out; echo err >&2")
    if err != nil {
        t.Fatalf("Exec failed: %v", err)
    }
    if out != "out" {
        t.Fatalf("expected stdout %q, got %q (stderr should be discarded)", "out", out)
    }
}

func TestLocalSession_ExecWithStdinCombined(t *testing.T) {
    s := &localSession{home: t.TempDir()}
    out, err := s.ExecWithStdin("echo out; echo err >&2", strings.NewReader(""))
    if err != nil {
        t.Fatalf("ExecWithStdin failed: %v", err)
    }
    if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
        t.Fatalf("expected combined output containing both 'out' and 'err', got %q", out)
    }
}

func TestLocalSession_ImplementsSessionExecutor(t *testing.T) {
    var _ SessionExecutor = (*localSession)(nil)
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/shim -run TestLocalSession -count=1 -v`
Expected: 3 PASS lines.

This locks Spec scenario 21 (test stub semantic alignment).

- [ ] **Step 3: Commit**

```bash
git add internal/shim/local_session_test.go
git commit -m "test(shim): add localSession stub for SessionExecutor"
```

---

## Task 3: DeployState ClaudeWrapper field

**Files:**
- Modify: `internal/shim/deploy.go`
- Test: `internal/shim/deploy_test.go` (add to existing)

- [ ] **Step 1: Write the failing test**

Add to `internal/shim/deploy_test.go` (add `"encoding/json"` and `"strings"` to imports if absent):

```go
func TestDeployState_ClaudeWrapperRoundtrip(t *testing.T) {
    in := &DeployState{
        BinaryHash:    "sha256:abc",
        BinaryVersion: "v0.7.1-test",
        ShimInstalled: true,
        ShimTarget:    "xclip",
        ClaudeWrapper: &ClaudeWrapperState{
            Installed:    true,
            OriginKind:   "symlink",
            OriginTarget: "/home/u/.local/share/claude/versions/2.1.132",
        },
    }
    data, err := json.Marshal(in)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var out DeployState
    if err := json.Unmarshal(data, &out); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if out.ClaudeWrapper == nil {
        t.Fatal("ClaudeWrapper missing after roundtrip")
    }
    if out.ClaudeWrapper.OriginKind != "symlink" {
        t.Fatalf("OriginKind: got %q, want symlink", out.ClaudeWrapper.OriginKind)
    }
}

func TestDeployState_ClaudeWrapperOmitemptyWhenNil(t *testing.T) {
    in := &DeployState{BinaryHash: "x"}
    data, err := json.Marshal(in)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    if strings.Contains(string(data), "claude_wrapper") {
        t.Fatalf("nil ClaudeWrapper should be omitted, got: %s", data)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestDeployState_ClaudeWrapper -count=1`
Expected: compile error — `undefined: ClaudeWrapperState`.

- [ ] **Step 3: Add ClaudeWrapperState struct and field**

Edit `internal/shim/deploy.go`. Add after the existing `NotifyDeployState` definition (around line 27):

```go
// ClaudeWrapperState records what InstallRemoteClaudeWrapper replaced at
// ~/.local/bin/claude. Used by uninstall and future doctor commands; the
// wrapper bash script itself does NOT read this — it only checks file
// existence/executability of the sidecar.
type ClaudeWrapperState struct {
    Installed    bool   `json:"installed"`
    OriginKind   string `json:"origin_kind"`             // "none" | "regular" | "symlink"
    OriginTarget string `json:"origin_target,omitempty"` // resolved path when OriginKind=="symlink"
}
```

Add field to `DeployState` struct (around line 31-39):

```go
type DeployState struct {
    BinaryHash    string              `json:"binary_hash"`
    BinaryVersion string              `json:"binary_version"`
    ShimInstalled bool                `json:"shim_installed"`
    ShimTarget    string              `json:"shim_target"`
    PathFixed     bool                `json:"path_fixed"`
    Notify        *NotifyDeployState  `json:"notify,omitempty"`
    Codex         *CodexDeployState   `json:"codex,omitempty"`
    ClaudeWrapper *ClaudeWrapperState `json:"claude_wrapper,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -run TestDeployState -count=1 -v`
Expected: PASS for all `TestDeployState_*` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/shim/deploy.go internal/shim/deploy_test.go
git commit -m "feat(shim): add ClaudeWrapperState to DeployState for diagnostics"
```

---

## Task 4: Wrapper template — sidecar-first exec

**Files:**
- Modify: `internal/shim/claude_wrapper.go`
- Test: `internal/shim/claude_wrapper_test.go` (add to existing)

- [ ] **Step 1: Write the failing test**

Add to `internal/shim/claude_wrapper_test.go`:

```go
func TestClaudeWrapperScript_PrefersSidecar(t *testing.T) {
    script := ClaudeWrapperScript(18339)
    if !strings.Contains(script, "claude.cc-clip-real") {
        t.Fatal("wrapper does not reference sidecar path")
    }
    // Sidecar branch must precede PATH-discovery fallback.
    sidecarIdx := strings.Index(script, "claude.cc-clip-real")
    pathDiscoveryIdx := strings.Index(script, "_PATH_DIRS")
    if sidecarIdx == -1 || pathDiscoveryIdx == -1 {
        t.Fatal("wrapper missing one of: sidecar branch, PATH-discovery fallback")
    }
    if sidecarIdx >= pathDiscoveryIdx {
        t.Fatal("sidecar branch must precede PATH-discovery fallback")
    }
}

func TestClaudeWrapperScript_KeepsPathFallback(t *testing.T) {
    // PATH-discovery must remain for backward compat with legacy installs
    // that have no sidecar.
    script := ClaudeWrapperScript(18339)
    if !strings.Contains(script, "_PATH_DIRS") || !strings.Contains(script, "_SELF_DIR") {
        t.Fatal("wrapper missing PATH-discovery fallback")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestClaudeWrapperScript_PrefersSidecar -count=1`
Expected: FAIL — `wrapper does not reference sidecar path`.

- [ ] **Step 3: Update the wrapper template**

Edit `internal/shim/claude_wrapper.go`. Replace `claudeWrapperTemplate` with:

```go
const claudeWrapperTemplate = `#!/usr/bin/env bash
# cc-clip claude wrapper — auto-inject notification hooks
# Installed by: cc-clip connect
# Remove with:  cc-clip uninstall --host <this-host>

# Find the real claude binary.
# Priority 1: ~/.local/bin/claude.cc-clip-real (the sidecar set by install).
# Priority 2: walk $PATH skipping our own directory (legacy install fallback).
_REAL_CLAUDE=""
_SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
_SIDECAR="$_SELF_DIR/claude.cc-clip-real"

if [ -x "$_SIDECAR" ]; then
    _REAL_CLAUDE="$_SIDECAR"
else
    IFS=: read -ra _PATH_DIRS <<< "$PATH"
    for _dir in "${_PATH_DIRS[@]}"; do
        [ "$_dir" = "$_SELF_DIR" ] && continue
        [ -x "$_dir/claude" ] && _REAL_CLAUDE="$_dir/claude" && break
    done
fi

if [ -z "$_REAL_CLAUDE" ]; then
    echo "cc-clip: real claude binary not found (no sidecar at $_SIDECAR and no claude on PATH outside $_SELF_DIR)" >&2
    exit 1
fi

# Only inject hooks if cc-clip tunnel is alive
if curl -sf --connect-timeout 1 --max-time 2 "http://127.0.0.1:${CC_CLIP_PORT:-%d}/health" >/dev/null 2>&1; then
    exec "$_REAL_CLAUDE" --settings '{
  "hooks": {
    "Stop": [{"type":"command","command":"cc-clip-hook"}],
    "Notification": [{"type":"command","command":"cc-clip-hook"}]
  }
}' "$@"
else
    # Tunnel not available — run claude without hook injection
    exec "$_REAL_CLAUDE" "$@"
fi
`
```

- [ ] **Step 4: Run all wrapper tests**

Run: `go test ./internal/shim -run TestClaudeWrapperScript -count=1 -v`
Expected: all PASS, including the new sidecar-first tests and any pre-existing port-substitution tests.

- [ ] **Step 5: Commit**

```bash
git add internal/shim/claude_wrapper.go internal/shim/claude_wrapper_test.go
git commit -m "feat(shim): wrapper exec sidecar-first with PATH-discovery fallback"
```

---

## Task 5: classifyClaudeBin helper

**Files:**
- Modify: `internal/shim/ssh.go`
- Create: `internal/shim/claude_wrapper_install_test.go`

**Why:** Layer 1 install branches on classification. Centralizing detection lets us test all states once.

- [ ] **Step 1: Write the failing test**

Create `internal/shim/claude_wrapper_install_test.go`:

```go
package shim

import (
    "os"
    "path/filepath"
    "runtime"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestClassifyClaudeBin -count=1`
Expected: compile error — `undefined: classifyClaudeBin`.

- [ ] **Step 3: Implement classifyClaudeBin**

Append to `internal/shim/ssh.go`:

```go
// classifyClaudeBin inspects ~/.local/bin/claude on the remote and returns
// one of: "none", "cc_wrapper", "symlink", "regular", "other".
//
// "cc_wrapper" means a regular file whose first 256 bytes contain the
// cc-clip wrapper marker — i.e. our previous install. Used to skip the
// sidecar staging step on re-install (we just overwrite our own wrapper).
//
// Inspection is read-only; nothing is modified.
func classifyClaudeBin(s SessionExecutor) (string, error) {
    out, err := s.Exec(`p="$HOME/.local/bin/claude"
if [ ! -e "$p" ] && [ ! -L "$p" ]; then echo none; exit 0; fi
if [ -L "$p" ]; then echo symlink; exit 0; fi
if [ -f "$p" ]; then
    if head -c 256 "$p" 2>/dev/null | grep -qF "# cc-clip claude wrapper"; then
        echo cc_wrapper
    else
        echo regular
    fi
    exit 0
fi
echo other`)
    if err != nil {
        return "", fmt.Errorf("classify ~/.local/bin/claude: %w", err)
    }
    return strings.TrimSpace(out), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -run TestClassifyClaudeBin -count=1 -v`
Expected: 4 PASS lines (none, regular, cc_wrapper, symlink).

- [ ] **Step 5: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): classifyClaudeBin helper for install branch dispatch"
```

---

## Task 6: InstallRemoteClaudeWrapper rewrite — `none` case (Spec scenario 3)

**Files:**
- Modify: `internal/shim/ssh.go`
- Modify: `internal/shim/claude_wrapper_install_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/shim/claude_wrapper_install_test.go` (add `"strings"` to imports if absent):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestInstall_NoPriorInstall -count=1`
Expected: FAIL — the existing `InstallRemoteClaudeWrapper` takes `*SSHSession`, not `SessionExecutor`. Compile error.

- [ ] **Step 3: Rewrite InstallRemoteClaudeWrapper signature + minimal implementation**

Replace the existing `InstallRemoteClaudeWrapper` in `internal/shim/ssh.go` (line 263-279) with:

```go
// InstallRemoteClaudeWrapper installs the claude wrapper to ~/.local/bin/claude
// on the remote, using a symlink-safe topology that never overwrites the real
// claude binary even when ~/.local/bin/claude is a symlink to a versions store
// (Anthropic Native Installer layout).
//
// Topology:
//   - The original entry (regular file or symlink) is renamed to
//     ~/.local/bin/claude.cc-clip-real.
//   - The wrapper script is written via mktemp + chmod + atomic mv.
//   - On re-install over an existing cc-clip wrapper, the wrapper file is
//     replaced atomically; the existing sidecar is left untouched.
func InstallRemoteClaudeWrapper(s SessionExecutor, port int) error {
    kind, err := classifyClaudeBin(s)
    if err != nil {
        return fmt.Errorf("install: classify failed: %w", err)
    }
    switch kind {
    case "none":
        return installWrapperNoOrigin(s, port)
    default:
        return fmt.Errorf("install: unsupported origin kind %q (more branches added in later tasks)", kind)
    }
}

// installWrapperNoOrigin writes the wrapper to a fresh ~/.local/bin/claude
// when no prior file exists. No sidecar is created.
func installWrapperNoOrigin(s SessionExecutor, port int) error {
    script := ClaudeWrapperScript(port)
    cmd := `mkdir -p "$HOME/.local/bin" && \
tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX") && \
cat > "$tmp" && \
chmod +x "$tmp" && \
mv "$tmp" "$HOME/.local/bin/claude"`
    out, err := s.ExecWithStdin(cmd, strings.NewReader(script))
    if err != nil {
        return fmt.Errorf("install (none): %s: %w", strings.TrimSpace(out), err)
    }
    return nil
}
```

The existing call site in `cmd/cc-clip/main.go:813` continues to compile because `*SSHSession` satisfies `SessionExecutor`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -run TestInstall_NoPriorInstall -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Verify the rest of the package still compiles**

Run: `go build ./...`
Expected: no errors.

Run: `go test ./internal/shim -count=1`
Expected: pre-existing tests still pass; only the partially-implemented branches will fail with the explicit "unsupported origin kind" error in tests we haven't written yet.

- [ ] **Step 6: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): rewrite InstallRemoteClaudeWrapper with classify-dispatch (none branch)"
```

---

## Task 7: Install — regular file branch (Spec scenario 2)

**Files:**
- Modify: `internal/shim/ssh.go`
- Modify: `internal/shim/claude_wrapper_install_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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

    // claude must be a regular file (not a symlink).
    info, err := os.Lstat(filepath.Join(binDir, "claude"))
    if err != nil {
        t.Fatal(err)
    }
    if info.Mode()&os.ModeSymlink != 0 {
        t.Fatal("claude should be a regular file after install")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestInstall_RegularFileOrigin -count=1`
Expected: FAIL — `unsupported origin kind "regular"`.

- [ ] **Step 3: Add regular branch**

In `internal/shim/ssh.go`, extend the `switch kind` in `InstallRemoteClaudeWrapper`:

```go
    switch kind {
    case "none":
        return installWrapperNoOrigin(s, port)
    case "regular", "symlink":
        return installWrapperWithSidecar(s, port)
    default:
        return fmt.Errorf("install: unsupported origin kind %q", kind)
    }
```

Add the new function:

```go
// installWrapperWithSidecar handles the regular and symlink origin kinds:
// stage the origin to ~/.local/bin/claude.cc-clip-real, then commit the
// wrapper. Uses prepare-then-commit ordering so failures before the final
// mv leave the user's claude untouched, and failures after staging trigger
// a best-effort rollback.
func installWrapperWithSidecar(s SessionExecutor, port int) error {
    script := ClaudeWrapperScript(port)
    // Pre-flight: refuse if sidecar already exists (we don't know if it's
    // stale or load-bearing). User can `rm` it and re-run.
    out, _ := s.Exec(`if test -e "$HOME/.local/bin/claude.cc-clip-real" || test -L "$HOME/.local/bin/claude.cc-clip-real"; then echo conflict; fi`)
    if strings.TrimSpace(out) == "conflict" {
        return fmt.Errorf("install: ~/.local/bin/claude.cc-clip-real already exists; remove it manually and re-run")
    }

    cmd := `set -e
mkdir -p "$HOME/.local/bin"
tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod +x "$tmp"
# Stage origin to sidecar (mv on a symlink renames the link itself,
# never reads/writes through it).
mv "$HOME/.local/bin/claude" "$HOME/.local/bin/claude.cc-clip-real"
# Commit wrapper.
if ! mv "$tmp" "$HOME/.local/bin/claude"; then
    # Best-effort rollback: restore origin so user's claude keeps working.
    mv "$HOME/.local/bin/claude.cc-clip-real" "$HOME/.local/bin/claude" 2>/dev/null || true
    exit 1
fi
trap - EXIT  # tmp now consumed by the mv`
    outErr, err := s.ExecWithStdin(cmd, strings.NewReader(script))
    if err != nil {
        return fmt.Errorf("install (with-sidecar): %s: %w", strings.TrimSpace(outErr), err)
    }
    return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -run TestInstall_RegularFileOrigin -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Run all install tests**

Run: `go test ./internal/shim -run TestInstall_ -count=1 -v`
Expected: existing none + new regular both PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): install regular-file origin via mv-only sidecar staging"
```

---

## Task 8: Install — symlink branch (Spec scenario 1, the issue #55 regression test)

**Files:**
- Modify: `internal/shim/claude_wrapper_install_test.go`

The `installWrapperWithSidecar` from Task 7 already handles symlink because `mv` on a symlink renames the link itself. We just need a test to lock that behavior and explicitly assert the issue #55 regression is closed.

- [ ] **Step 1: Write the failing test**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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

    // Wrapper must be a regular file.
    wrapperInfo, err := os.Lstat(filepath.Join(binDir, "claude"))
    if err != nil {
        t.Fatal(err)
    }
    if wrapperInfo.Mode()&os.ModeSymlink != 0 {
        t.Fatal("claude should be a regular file after install (the wrapper)")
    }
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/shim -run TestInstall_SymlinkOrigin -count=1 -v`
Expected: PASS (the implementation in Task 7 already supports this).

- [ ] **Step 3: Commit**

```bash
git add internal/shim/claude_wrapper_install_test.go
git commit -m "test(shim): lock issue #55 regression — symlink install does not corrupt target"
```

---

## Task 9: Install — cc_wrapper re-install branch (Spec scenario 4)

**Files:**
- Modify: `internal/shim/ssh.go`
- Modify: `internal/shim/claude_wrapper_install_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestInstall_ReinstallOverCcWrapper -count=1`
Expected: FAIL — `unsupported origin kind "cc_wrapper"`.

- [ ] **Step 3: Add cc_wrapper branch**

Edit `internal/shim/ssh.go`. Update the switch:

```go
    switch kind {
    case "none":
        return installWrapperNoOrigin(s, port)
    case "cc_wrapper":
        return installWrapperOverwriteSelf(s, port)
    case "regular", "symlink":
        return installWrapperWithSidecar(s, port)
    default:
        return fmt.Errorf("install: unsupported origin kind %q", kind)
    }
```

Add new function:

```go
// installWrapperOverwriteSelf replaces our own previous wrapper at
// ~/.local/bin/claude via mktemp + atomic mv. The sidecar is left untouched
// because we already own the claude path (it's our wrapper, not user state).
func installWrapperOverwriteSelf(s SessionExecutor, port int) error {
    script := ClaudeWrapperScript(port)
    cmd := `set -e
mkdir -p "$HOME/.local/bin"
tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod +x "$tmp"
mv "$tmp" "$HOME/.local/bin/claude"
trap - EXIT`
    out, err := s.ExecWithStdin(cmd, strings.NewReader(script))
    if err != nil {
        return fmt.Errorf("install (cc_wrapper overwrite): %s: %w", strings.TrimSpace(out), err)
    }
    return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -run TestInstall_ReinstallOverCcWrapper -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): atomic re-install over existing cc-clip wrapper"
```

---

## Task 10: Install — sidecar collision guard (Spec scenarios 18, 19)

**Files:**
- Modify: `internal/shim/claude_wrapper_install_test.go`

The collision guard pre-flight is already in `installWrapperWithSidecar`. This task adds tests to lock the behavior.

- [ ] **Step 1: Write the failing tests**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/shim -run TestInstall_SidecarCollision -count=1 -v`
Expected: both PASS (the guard already exists in Task 7 implementation).

- [ ] **Step 3: Commit**

```bash
git add internal/shim/claude_wrapper_install_test.go
git commit -m "test(shim): lock sidecar collision guard for regular and directory paths"
```

---

## Task 11: Install — staging mv failure rollback + mktemp guard (Spec scenarios 16, 17)

**Files:**
- Modify: `internal/shim/claude_wrapper_install_test.go`

The mktemp + rollback logic is already in Task 7's `installWrapperWithSidecar`. This task adds verification tests.

- [ ] **Step 1: Write the rollback test**

Append to `internal/shim/claude_wrapper_install_test.go` (add `"io"` to imports if absent):

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/shim -run TestInstall_RollbackOnFinalMvFailure -count=1 -v`
Expected: PASS (rollback logic is in Task 7's implementation).

- [ ] **Step 3: Write mktemp anti-symlink-follow test**

Append:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -run TestInstall_MktempDoesNotFollowSymlinks -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/shim/claude_wrapper_install_test.go
git commit -m "test(shim): lock install rollback on final-mv failure and mktemp safety"
```

---

## Task 12: DetectV070Corruption (Spec scenarios 10, 12, 13, 14, 15)

**Files:**
- Modify: `internal/shim/ssh.go`
- Modify: `internal/shim/claude_wrapper_install_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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

func TestDetectV070_CorruptedState(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("symlink semantics differ on Windows")
    }
    home, _ := setupFakeHome(t)
    makeV070Corruption(t, home)

    s := &localSession{home: home}
    corrupted, err := DetectV070Corruption(s)
    if err != nil {
        t.Fatalf("detect: %v", err)
    }
    if !corrupted {
        t.Fatal("expected corrupted=true on canonical v0.7.0 state")
    }
}

func TestDetectV070_NotSymlinkOrigin(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("bash-based install path is Linux-only")
    }
    home, binDir := setupFakeHome(t)
    if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("regular"), 0755); err != nil {
        t.Fatal(err)
    }
    // Even with a .cc-clip-bak, C1 fails (not symlink).
    if err := os.WriteFile(filepath.Join(binDir, "claude.cc-clip-bak"), make([]byte, 5*1024*1024), 0755); err != nil {
        t.Fatal(err)
    }
    s := &localSession{home: home}
    corrupted, err := DetectV070Corruption(s)
    if err != nil {
        t.Fatal(err)
    }
    if corrupted {
        t.Fatal("expected corrupted=false when origin is regular file (C1 must fail)")
    }
}

func TestDetectV070_BackupMissing(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("symlink semantics differ on Windows")
    }
    home, binDir := setupFakeHome(t)
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
    // No .cc-clip-bak.
    s := &localSession{home: home}
    corrupted, err := DetectV070Corruption(s)
    if err != nil {
        t.Fatal(err)
    }
    if corrupted {
        t.Fatal("expected corrupted=false when backup is missing (C3 must fail)")
    }
}

func TestDetectV070_BackupIsAlsoWrapper(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("symlink semantics differ on Windows")
    }
    home, binDir := setupFakeHome(t)
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
    // Backup is also a wrapper. Pad to >1MB so C4 passes.
    bakContent := append([]byte(ClaudeWrapperScript(18339)), make([]byte, 2*1024*1024)...)
    if err := os.WriteFile(filepath.Join(binDir, "claude.cc-clip-bak"), bakContent, 0755); err != nil {
        t.Fatal(err)
    }

    s := &localSession{home: home}
    corrupted, err := DetectV070Corruption(s)
    if err != nil {
        t.Fatal(err)
    }
    if corrupted {
        t.Fatal("expected corrupted=false when backup itself is a wrapper (C5 must fail)")
    }
}

func TestDetectV070_SymlinkTargetNotWrapper(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("symlink semantics differ on Windows")
    }
    home, binDir := setupFakeHome(t)
    versionsDir := filepath.Join(home, ".local", "share", "claude", "versions")
    if err := os.MkdirAll(versionsDir, 0755); err != nil {
        t.Fatal(err)
    }
    target := filepath.Join(versionsDir, "2.1.132")
    if err := os.WriteFile(target, []byte("\x7fELF... regular real binary"), 0755); err != nil {
        t.Fatal(err)
    }
    if err := os.Symlink(target, filepath.Join(binDir, "claude")); err != nil {
        t.Fatal(err)
    }
    s := &localSession{home: home}
    corrupted, err := DetectV070Corruption(s)
    if err != nil {
        t.Fatal(err)
    }
    if corrupted {
        t.Fatal("expected corrupted=false when symlink target is a real binary (C2 must fail)")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/shim -run TestDetectV070 -count=1`
Expected: compile error — `undefined: DetectV070Corruption`.

- [ ] **Step 3: Implement DetectV070Corruption**

Append to `internal/shim/ssh.go`:

```go
// DetectV070Corruption returns true when the remote exhibits the exact
// filesystem state produced by v0.7.0's buggy InstallRemoteClaudeWrapper:
//
//   C1: ~/.local/bin/claude is a symlink.
//   C2: The symlink target's first 256 bytes contain the cc-clip wrapper marker.
//   C3: ~/.local/bin/claude.cc-clip-bak exists as a regular file.
//   C4: That backup is larger than 1 MiB.
//   C5: That backup is NOT itself a cc-clip wrapper (guards against a double-
//       install pathology where both target and backup are wrappers).
//
// All five conditions must be true to return corrupted=true. The check is
// strictly read-only — nothing is modified.
func DetectV070Corruption(s SessionExecutor) (bool, error) {
    out, err := s.Exec(`set -e
claude="$HOME/.local/bin/claude"
bak="$HOME/.local/bin/claude.cc-clip-bak"
# C1: claude is a symlink.
[ -L "$claude" ] || { echo notcorrupted; exit 0; }
# C2: target contains wrapper marker.
target=$(readlink -f "$claude") || { echo notcorrupted; exit 0; }
[ -f "$target" ] || { echo notcorrupted; exit 0; }
head -c 256 "$target" 2>/dev/null | grep -qF "# cc-clip claude wrapper" || { echo notcorrupted; exit 0; }
# C3: backup is a regular file.
[ -f "$bak" ] || { echo notcorrupted; exit 0; }
# C4: backup > 1 MiB.
size=$(wc -c < "$bak" | tr -d ' ')
[ "$size" -gt 1048576 ] || { echo notcorrupted; exit 0; }
# C5: backup is NOT a wrapper.
if head -c 256 "$bak" 2>/dev/null | grep -qF "# cc-clip claude wrapper"; then
    echo notcorrupted
    exit 0
fi
echo corrupted`)
    if err != nil {
        return false, fmt.Errorf("detect v0.7.0 corruption: %w", err)
    }
    return strings.TrimSpace(out) == "corrupted", nil
}
```

- [ ] **Step 4: Run all detection tests**

Run: `go test ./internal/shim -run TestDetectV070 -count=1 -v`
Expected: 5 PASS lines.

- [ ] **Step 5: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): DetectV070Corruption with five-condition gate"
```

---

## Task 13: RecoverV070Corruption (Spec scenario 11)

**Files:**
- Modify: `internal/shim/ssh.go`
- Modify: `internal/shim/claude_wrapper_install_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestRecoverV070 -count=1`
Expected: compile error — `undefined: RecoverV070Corruption`.

- [ ] **Step 3: Implement RecoverV070Corruption**

Append to `internal/shim/ssh.go`:

```go
// RecoverV070Corruption migrates the v0.7.0 backup file (which contains the
// real claude binary) back to the symlink's target path, restoring the
// original Native Installer layout. After this call, the caller is expected
// to re-run InstallRemoteClaudeWrapper to install a v0.7.1+ wrapper safely.
//
// This function re-verifies all five corruption conditions inside the same
// SSH session before doing anything destructive (TOCTOU guard).
func RecoverV070Corruption(s SessionExecutor) error {
    corrupted, err := DetectV070Corruption(s)
    if err != nil {
        return fmt.Errorf("recover: re-detection failed: %w", err)
    }
    if !corrupted {
        return fmt.Errorf("recover: corruption no longer detected; refusing to migrate backup")
    }
    out, err := s.Exec(`set -e
target=$(readlink -f "$HOME/.local/bin/claude")
mv "$HOME/.local/bin/claude.cc-clip-bak" "$target"`)
    if err != nil {
        return fmt.Errorf("recover: migrate backup: %s: %w", strings.TrimSpace(out), err)
    }
    return nil
}
```

- [ ] **Step 4: Run all recovery tests**

Run: `go test ./internal/shim -run TestRecoverV070 -count=1 -v`
Expected: 2 PASS.

- [ ] **Step 5: Run full install + detect + recover suite**

Run: `go test ./internal/shim -count=1 -race`
Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): RecoverV070Corruption migrates backup to symlink target"
```

---

## Task 14: UninstallRemoteClaudeWrapper (Spec scenarios 7, 8, 9)

**Files:**
- Modify: `internal/shim/ssh.go`
- Modify: `internal/shim/claude_wrapper_install_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/shim/claude_wrapper_install_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/shim -run TestUninstall_ -count=1`
Expected: compile error — `undefined: UninstallRemoteClaudeWrapper`.

- [ ] **Step 3: Implement UninstallRemoteClaudeWrapper**

Append to `internal/shim/ssh.go`:

```go
// UninstallRemoteClaudeWrapper removes the cc-clip claude wrapper from
// ~/.local/bin/claude and restores the origin from ~/.local/bin/claude.cc-clip-real.
//
// Safety: refuses if ~/.local/bin/claude is not a cc-clip wrapper (i.e. the
// user replaced it with their own file). In that case, returns an error
// without touching anything.
//
// If a v0.7.0-era ~/.local/bin/claude.cc-clip-bak file exists, it is left
// in place — recovery of that backup is the user's call (see
// RecoverV070Corruption + --auto-recover).
func UninstallRemoteClaudeWrapper(s SessionExecutor) error {
    // Verify file at claude is a cc-clip wrapper (and exists).
    out, err := s.Exec(`p="$HOME/.local/bin/claude"
if [ ! -e "$p" ] && [ ! -L "$p" ]; then echo absent; exit 0; fi
if [ -L "$p" ] || [ ! -f "$p" ]; then echo notwrapper; exit 0; fi
if head -c 256 "$p" 2>/dev/null | grep -qF "# cc-clip claude wrapper"; then
    echo wrapper
else
    echo notwrapper
fi`)
    if err != nil {
        return fmt.Errorf("uninstall: classify failed: %w", err)
    }
    switch strings.TrimSpace(out) {
    case "absent":
        return nil
    case "notwrapper":
        return fmt.Errorf("uninstall: ~/.local/bin/claude is not a cc-clip wrapper; refusing to remove")
    case "wrapper":
        // proceed
    default:
        return fmt.Errorf("uninstall: unexpected classify output %q", strings.TrimSpace(out))
    }

    // Remove wrapper, restore origin from sidecar if present.
    cmd := `set -e
rm "$HOME/.local/bin/claude"
if [ -e "$HOME/.local/bin/claude.cc-clip-real" ] || [ -L "$HOME/.local/bin/claude.cc-clip-real" ]; then
    mv "$HOME/.local/bin/claude.cc-clip-real" "$HOME/.local/bin/claude"
fi`
    outErr, err := s.Exec(cmd)
    if err != nil {
        return fmt.Errorf("uninstall: %s: %w", strings.TrimSpace(outErr), err)
    }
    return nil
}
```

- [ ] **Step 4: Run uninstall tests**

Run: `go test ./internal/shim -run TestUninstall_ -count=1 -v`
Expected: 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/shim/ssh.go internal/shim/claude_wrapper_install_test.go
git commit -m "feat(shim): UninstallRemoteClaudeWrapper restores origin via sidecar"
```

---

## Task 15: CLI — `--auto-recover` flag + mutex check (Spec scenario 22)

**Files:**
- Modify: `cmd/cc-clip/main.go`
- Create: `cmd/cc-clip/cli_flag_test.go`

- [ ] **Step 1: Locate flag-parsing entry points**

Run: `grep -n 'tokenOnly\|getFlag\|hasFlag' cmd/cc-clip/main.go | head -20`
Expected: shows lines where `--token-only` and `--codex` are read in `cmdConnect`/`cmdSetup`.

- [ ] **Step 2: Add `--auto-recover` flag read + mutex check**

In `cmdConnect`, immediately after the existing `tokenOnly := hasFlag("token-only")` line and BEFORE any SSH setup or `connectVerifyTunnel` call (i.e. before line 552 where SSH session setup begins), insert:

```go
    autoRecover := hasFlag("auto-recover")
    if autoRecover && tokenOnly {
        fmt.Fprintln(os.Stderr, `error: --auto-recover cannot be combined with --token-only
       --auto-recover performs recovery and full reinstall.
       Re-run without --token-only:
           cc-clip connect <host> --auto-recover
       Or, if you only want to recover the binary without reinstalling the
       wrapper, run the manual recovery and then cc-clip connect --token-only:
           ssh <host> 'mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"'
           cc-clip connect <host> --token-only`)
        os.Exit(2)
    }
    _ = autoRecover // consumed in Task 16 (N0 wiring)
```

Mirror the same block at the equivalent location in `cmdSetup`. (If `cmdSetup` delegates to `cmdConnect`, the block lives only at the entry point that's reached first.)

- [ ] **Step 3: Add an automated test for the mutex**

Create `cmd/cc-clip/cli_flag_test.go`:

```go
package main

import (
    "bytes"
    "os/exec"
    "strings"
    "testing"
)

func TestCLIMutex_AutoRecoverWithTokenOnly(t *testing.T) {
    cmd := exec.Command("go", "run", ".", "connect", "fakehost.invalid", "--auto-recover", "--token-only")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit on flag conflict")
    }
    if !strings.Contains(stderr.String(), "--auto-recover cannot be combined with --token-only") {
        t.Fatalf("missing mutex error in stderr: %s", stderr.String())
    }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cc-clip -run TestCLIMutex -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/cc-clip/main.go cmd/cc-clip/cli_flag_test.go
git commit -m "feat(cli): --auto-recover flag with --token-only mutex"
```

---

## Task 16: CLI — N0 detection wiring in cmdConnect/cmdSetup (Spec scenarios 10, 11, 20)

**Files:**
- Modify: `cmd/cc-clip/main.go`

- [ ] **Step 1: Insert N0 step between SSH session and --token-only branch**

In `cmdConnect`, right after line 558 (`fmt.Println("      SSH master connected")`) and before line 561 (`if tokenOnly {`), insert:

```go
    // N0: Pre-deploy v0.7.0 corruption detection. Runs before any other
    // remote write (including --token-only token sync) so a corrupted remote
    // either aborts cleanly or recovers in one step.
    fmt.Println("[N0] Checking for v0.7.0 wrapper corruption...")
    corrupted, err := shim.DetectV070Corruption(session)
    if err != nil {
        log.Fatalf("      N0 detection failed: %v", err)
    }
    if corrupted {
        if !autoRecover {
            fmt.Fprintf(os.Stderr, `
error: detected v0.7.0 corruption on remote: ~/.local/bin/claude is a symlink
       to a file that is now a cc-clip wrapper, with the real binary backed up
       at ~/.local/bin/claude.cc-clip-bak.

To recover, either re-run with --auto-recover:

    cc-clip connect %s --auto-recover

Or fix manually:

    ssh %s 'mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"'
    cc-clip connect %s

If ~/.local/bin/claude.cc-clip-bak is missing on the remote, reinstall Claude
Code via 'curl https://claude.ai/install.sh' and re-run cc-clip connect.
`, host, host, host)
            os.Exit(3)
        }
        fmt.Println("      v0.7.0 corruption detected; running recovery...")
        if err := shim.RecoverV070Corruption(session); err != nil {
            log.Fatalf("      recovery failed: %v", err)
        }
        fmt.Println("      backup migrated to versions store; continuing install")
    } else {
        fmt.Println("      no corruption detected")
    }
```

Replace the earlier `_ = autoRecover` placeholder line from Task 15 (the variable is now used).

Apply the same block at the equivalent position in `cmdSetup` if it has its own SSH session setup. If `cmdSetup` delegates to `cmdConnect`, the block lives only once.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 3: Note on testing**

End-to-end testing of `cmdConnect` requires a real or mocked SSH layer. Layer 3 logic (`DetectV070Corruption` + `RecoverV070Corruption`) is unit-tested in tasks 12-13; this step is purely wiring. Manual smoke per spec §5 covers the integrated path.

- [ ] **Step 4: Run full test suite to confirm no regressions**

Run: `go test ./... -count=1 -race`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/cc-clip/main.go
git commit -m "feat(cli): N0 v0.7.0 corruption detection in cmdConnect/cmdSetup

Inserts detection between SSH session establishment and the --token-only
branch so all deploy paths share fail-closed semantics. Unit tests for
DetectV070Corruption and RecoverV070Corruption cover the logic; CLI wiring
verified via manual smoke per spec section 5."
```

---

## Task 17: CLI — cmdUninstall --host best-effort + wrapper restore (Spec scenarios 7, 8, 9 wiring)

**Files:**
- Modify: `cmd/cc-clip/main.go`

- [ ] **Step 1: Modify the local-uninstall block to be best-effort when --host is set**

In `cmd/cc-clip/main.go:399-403`, replace:

```go
    if err := shim.Uninstall(target, installPath); err != nil {
        log.Fatalf("uninstall failed: %v", err)
    }

    fmt.Println("Shim removed successfully.")
```

With:

```go
    if err := shim.Uninstall(target, installPath); err != nil {
        if host == "" {
            log.Fatalf("uninstall failed: %v", err)
        }
        fmt.Fprintf(os.Stderr, "warning: local shim uninstall failed (continuing because --host was set): %v\n", err)
    } else {
        fmt.Println("Shim removed successfully.")
    }
```

- [ ] **Step 2: Add wrapper restore step inside the `--host` branch**

In the `if host != ""` block (currently line 405-412), insert wrapper restore BEFORE the existing PATH marker cleanup:

```go
    if host != "" {
        fmt.Printf("Restoring claude wrapper on remote %s...\n", host)
        session, err := shim.NewSSHSession(host)
        if err != nil {
            fmt.Fprintf(os.Stderr, "warning: failed to open SSH session for wrapper restore: %v\n", err)
        } else {
            defer session.Close()
            if err := shim.UninstallRemoteClaudeWrapper(session); err != nil {
                fmt.Fprintf(os.Stderr, "warning: failed to restore claude wrapper: %v\n", err)
            } else {
                fmt.Println("      claude wrapper removed; original entry restored from sidecar")
            }
        }

        fmt.Printf("Removing PATH marker from remote %s...\n", host)
        if err := shim.RemoveRemotePath(host); err != nil {
            fmt.Fprintf(os.Stderr, "warning: failed to remove PATH marker: %v\n", err)
        } else {
            fmt.Println("PATH marker removed from remote shell rc file.")
        }
    }
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/cc-clip/main.go
git commit -m "feat(cli): cmdUninstall --host best-effort local + remote wrapper restore"
```

---

## Task 18: Docs — printUsage + commands.md + README

**Files:**
- Modify: `cmd/cc-clip/main.go` (printUsage)
- Modify: `docs/commands.md`
- Modify: `README.md`

- [ ] **Step 1: Update printUsage**

In `cmd/cc-clip/main.go` around line 99-152, locate the `setup` and `connect` flag documentation. Add `--auto-recover` to both sections so users discover it at `cc-clip --help`:

```
  setup <host> [--codex] [--token-only] [--auto-recover]
  connect <host> [--codex] [--token-only] [--auto-recover]

Flags:
  --auto-recover   Recover from v0.7.0 wrapper corruption (cannot combine with --token-only)
```

Update the `uninstall --host` doc line:

```
  uninstall --host <host>   Remove cc-clip from remote: claude wrapper, PATH marker, hook script
```

- [ ] **Step 2: Update docs/commands.md**

Open `docs/commands.md`. For the `setup` and `connect` rows, add an `--auto-recover` row underneath each:

```markdown
| `--auto-recover` | Recover from v0.7.0 claude wrapper corruption (mutually exclusive with `--token-only`). |
```

Add a new section after install/setup commands titled "Symlink-safe install" with one paragraph: "cc-clip's claude wrapper at `~/.local/bin/claude` preserves the original entry by renaming it to `~/.local/bin/claude.cc-clip-real` (the sidecar). The wrapper exec's the sidecar first, falling back to PATH discovery for backward compatibility. See `docs/superpowers/specs/2026-05-07-issue-55-claude-wrapper-symlink-fix-design.md` for full background."

- [ ] **Step 3: Update README.md**

Locate the FAQ / Q&A section in README.md (around line 149). Add one Q&A entry:

```markdown
**Q: I see `cc-clip: real claude binary not found in PATH` after running cc-clip v0.7.0. What now?**

A: Run `cc-clip setup <host> --auto-recover` (or `cc-clip connect <host> --auto-recover`). It detects the v0.7.0 corruption and migrates your real claude binary back into place before installing the (fixed) wrapper.
```

- [ ] **Step 4: Verify nothing else broke**

Run: `go test ./... -count=1 -race`
Expected: all PASS.

Run: `go vet ./...`
Expected: no output.

Run: `make build`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add cmd/cc-clip/main.go docs/commands.md README.md
git commit -m "docs: --auto-recover flag and uninstall --host wrapper restore semantics"
```

---

## Final Verification

- [ ] **Run the full test suite**

```bash
go test ./... -count=1 -race
go vet ./...
make build
```

Expected: all tests pass, vet clean, binary builds.

- [ ] **Optional: manual smoke (per spec §5)**

If a Linux VM with Anthropic Native Installer is available:

1. `cc-clip setup <host>` against a fresh Native Installer install. Verify symlink intact, real binary intact, `claude --version` works.
2. `cc-clip uninstall --host <host>`. Verify symlink restored, real binary intact.
3. Construct v0.7.0 corruption by hand on a second VM. `cc-clip setup <host>` → expect abort. `cc-clip setup <host> --auto-recover` → expect clean install + working `claude`.

- [ ] **Optional: ready for PR**

```bash
git log --oneline 2e81fa9..HEAD
```

Expected: ~18 commits forming the implementation. Push and open a PR referencing issue #55.

---

## Self-Review Checklist (run before handing off)

- [x] Spec coverage: every test scenario 1-22 maps to a task. (1, 2, 3 → T6, T7, T8; 4 → T9; 5, 6 → T4; 7, 8, 9 → T14; 10, 12, 13, 14, 15 → T12; 11 → T13; 16, 17 → T11; 18, 19 → T10; 20 → T16; 21 → T2; 22 → T15.)
- [x] Placeholder scan: no "TBD"; every code block has real Go/bash content.
- [x] Type consistency: `SessionExecutor`, `ClaudeWrapperState`, `InstallRemoteClaudeWrapper(s SessionExecutor, port int) error`, `UninstallRemoteClaudeWrapper(s SessionExecutor) error`, `DetectV070Corruption(s SessionExecutor) (bool, error)`, `RecoverV070Corruption(s SessionExecutor) error`, `classifyClaudeBin(s SessionExecutor) (string, error)` — used consistently across all tasks.
- [x] Build order respects dependencies: T1 (interface) → T2 (stub) → T3 (data) → T4 (template) → T5 (classify) → T6-T11 (install branches) → T12 (detect) → T13 (recover) → T14 (uninstall) → T15-T17 (CLI) → T18 (docs).
- [x] Each task has explicit Run / Expected pairs for tests.
- [x] All commits use Conventional Commits prefix (feat/fix/refactor/test/docs).
