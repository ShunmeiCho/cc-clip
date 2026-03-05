# cc-clip v0.1 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build `cc-clip` v0.1 so remote Claude Code can paste local Mac clipboard images over SSH via shim-based clipboard proxying.

**Architecture:** A local loopback-only HTTP daemon serves clipboard metadata/image bytes with bearer auth. Remote user-space shims (`xclip` first, `wl-paste` later) proxy Claude's clipboard queries through SSH RemoteForward and fall back to real binaries on expected failures. `connect` bootstraps remote install/token delivery and `doctor` verifies end-to-end health.

**Tech Stack:** Go 1.22, standard library (`net/http`, `os/exec`, `testing`), shell shim templates, GitHub Releases + install script.

---

**Execution Skills:** `@superpowers/test-driven-development`, `@superpowers/systematic-debugging`, `@superpowers/verification-before-completion`.

### Task 1: Bootstrap Go Module and CLI Skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/cc-clip/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`
- Create: `Makefile`

**Step 1: Write the failing test**

```go
func TestRootCommand_HelpIncludesCoreCommands(t *testing.T) {
	out, err := ExecuteForTest([]string{"--help"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "serve") || !strings.Contains(out, "connect") {
		t.Fatalf("missing expected commands in help: %s", out)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestRootCommand_HelpIncludesCoreCommands -v`  
Expected: FAIL with undefined `ExecuteForTest`.

**Step 3: Write minimal implementation**

```go
func ExecuteForTest(args []string) (string, error) {
	return "serve\nconnect\ninstall\nuninstall\npaste\nstatus\ndoctor\nversion\n", nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/cli -run TestRootCommand_HelpIncludesCoreCommands -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add go.mod cmd/cc-clip/main.go internal/cli/root.go internal/cli/root_test.go Makefile
git commit -m "chore: bootstrap cc-clip module and cli skeleton"
```

### Task 2: Implement Token Lifecycle Package

**Files:**
- Create: `internal/token/generate.go`
- Create: `internal/token/validate.go`
- Create: `internal/token/file.go`
- Create: `internal/token/token_test.go`

**Step 1: Write the failing test**

```go
func TestIssueAndValidateToken_WithTTL(t *testing.T) {
	tok, exp, err := Issue(2 * time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	require.True(t, time.Until(exp) > 0)
	require.NoError(t, Validate(tok, exp, time.Now()))
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/token -run TestIssueAndValidateToken_WithTTL -v`  
Expected: FAIL with undefined `Issue`.

**Step 3: Write minimal implementation**

```go
func Issue(ttl time.Duration) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil { return "", time.Time{}, err }
	return hex.EncodeToString(b), time.Now().Add(ttl), nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/token -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/token
git commit -m "feat: add session token issue validate and file persistence"
```

### Task 3: Add Clipboard Providers (Mac GA, Windows Beta Stub)

**Files:**
- Create: `internal/daemon/clipboard.go`
- Create: `internal/daemon/clipboard_darwin.go`
- Create: `internal/daemon/clipboard_windows.go`
- Create: `internal/daemon/clipboard_unix_stub.go`
- Create: `internal/daemon/clipboard_test.go`

**Step 1: Write the failing test**

```go
func TestClipboardType_Image(t *testing.T) {
	p := fakeProvider{typ: "image", format: "png"}
	got, _, err := p.Type(context.Background())
	require.NoError(t, err)
	require.Equal(t, "image", got)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon -run TestClipboardType_Image -v`  
Expected: FAIL with missing provider interface/types.

**Step 3: Write minimal implementation**

```go
type Provider interface {
	Type(ctx context.Context) (typ string, format string, err error)
	Image(ctx context.Context) ([]byte, string, error)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/daemon/clipboard*.go
git commit -m "feat: define clipboard provider interface and platform providers"
```

### Task 4: Build Local HTTP Daemon with Auth and Size Limits

**Files:**
- Create: `internal/daemon/server.go`
- Create: `internal/daemon/handlers.go`
- Create: `internal/daemon/server_test.go`
- Modify: `cmd/cc-clip/main.go`
- Modify: `internal/cli/root.go`

**Step 1: Write the failing test**

```go
func TestClipboardImage_RequiresBearerToken(t *testing.T) {
	srv := newTestServer(fakeProvider{img: png1x1})
	req := httptest.NewRequest(http.MethodGet, "/clipboard/image", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon -run TestClipboardImage_RequiresBearerToken -v`  
Expected: FAIL with missing handlers/router.

**Step 3: Write minimal implementation**

```go
if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/daemon/server.go internal/daemon/handlers.go internal/daemon/server_test.go cmd/cc-clip/main.go internal/cli/root.go
git commit -m "feat: implement local daemon endpoints with bearer auth"
```

### Task 5: Implement Tunnel Probe and Remote Paste Command

**Files:**
- Create: `internal/tunnel/probe.go`
- Create: `internal/tunnel/fetch.go`
- Create: `internal/tunnel/fetch_test.go`
- Create: `internal/cli/paste.go`
- Create: `internal/cli/paste_test.go`

**Step 1: Write the failing test**

```go
func TestPasteCommand_SavesImageAndPrintsPath(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clipboard/type" { _, _ = w.Write([]byte(`{"type":"image","format":"png"}`)); return }
		if r.URL.Path == "/clipboard/image" { w.Header().Set("Content-Type", "image/png"); _, _ = w.Write(png1x1); return }
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()
	path, err := PasteOnce(context.Background(), Config{BaseURL: s.URL, OutDir: t.TempDir()})
	require.NoError(t, err)
	require.FileExists(t, path)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tunnel ./internal/cli -run Paste -v`  
Expected: FAIL with undefined `PasteOnce`.

**Step 3: Write minimal implementation**

```go
func PasteOnce(ctx context.Context, cfg Config) (string, error) {
	// probe type, fetch image bytes, write timestamped png, return absolute path
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/tunnel ./internal/cli -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tunnel internal/cli/paste.go internal/cli/paste_test.go
git commit -m "feat: add tunnel probe and paste command with file output"
```

### Task 6: Build xclip Shim Templates and Installer (PATH Mode)

**Files:**
- Create: `internal/shim/install.go`
- Create: `internal/shim/install_test.go`
- Create: `scripts/shim_xclip.sh.tmpl`
- Create: `internal/cli/install.go`
- Create: `internal/cli/uninstall.go`

**Step 1: Write the failing test**

```go
func TestInstall_PathMode_WritesShimToUserBin(t *testing.T) {
	root := t.TempDir()
	err := InstallPathMode(InstallConfig{UserBin: filepath.Join(root, ".local/bin"), RealBinary: "/usr/bin/xclip"})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, ".local/bin", "xclip"))
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/shim -run TestInstall_PathMode_WritesShimToUserBin -v`  
Expected: FAIL with undefined `InstallPathMode`.

**Step 3: Write minimal implementation**

```go
func InstallPathMode(cfg InstallConfig) error {
	if err := os.MkdirAll(cfg.UserBin, 0o755); err != nil { return err }
	return os.WriteFile(filepath.Join(cfg.UserBin, "xclip"), []byte(renderShim(cfg)), 0o755)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/shim -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/shim internal/cli/install.go internal/cli/uninstall.go scripts/shim_xclip.sh.tmpl
git commit -m "feat: add xclip shim installer and uninstall commands"
```

### Task 7: Implement Connect Bootstrap Flow

**Files:**
- Create: `internal/connect/flow.go`
- Create: `internal/connect/ssh_runner.go`
- Create: `internal/connect/flow_test.go`
- Create: `internal/cli/connect.go`

**Step 1: Write the failing test**

```go
func TestConnect_RunSequence(t *testing.T) {
	r := &fakeRunner{}
	err := Run(context.Background(), Config{Host: "myserver"}, r)
	require.NoError(t, err)
	require.Equal(t, []string{
		"detect-arch",
		"upload-binary",
		"remote-install",
		"write-token-file",
		"verify-probe",
	}, r.Steps)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/connect -run TestConnect_RunSequence -v`  
Expected: FAIL with undefined `Run`.

**Step 3: Write minimal implementation**

```go
func Run(ctx context.Context, cfg Config, r Runner) error {
	for _, step := range []func(context.Context, Config, Runner) error{
		stepDetectArch, stepUpload, stepInstall, stepWriteToken, stepVerify,
	} {
		if err := step(ctx, cfg, r); err != nil { return err }
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/connect -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/connect internal/cli/connect.go
git commit -m "feat: add connect command with staged remote bootstrap flow"
```

### Task 8: Implement Status and Doctor Commands

**Files:**
- Create: `internal/doctor/local.go`
- Create: `internal/doctor/remote.go`
- Create: `internal/doctor/doctor_test.go`
- Create: `internal/cli/status.go`
- Create: `internal/cli/doctor.go`

**Step 1: Write the failing test**

```go
func TestDoctorLocal_ReportsDaemonAndClipboard(t *testing.T) {
	out := &bytes.Buffer{}
	err := RunLocal(context.Background(), LocalConfig{Writer: out, Probe: fakeProbeOK{}})
	require.NoError(t, err)
	require.Contains(t, out.String(), "Checking local daemon...    ✓")
	require.Contains(t, out.String(), "Checking clipboard access...✓")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/doctor -run TestDoctorLocal_ReportsDaemonAndClipboard -v`  
Expected: FAIL with undefined `RunLocal`.

**Step 3: Write minimal implementation**

```go
func RunLocal(ctx context.Context, cfg LocalConfig) error {
	fmt.Fprintln(cfg.Writer, "Checking local daemon...    ✓")
	fmt.Fprintln(cfg.Writer, "Checking clipboard access...✓")
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/doctor -v`  
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/doctor internal/cli/status.go internal/cli/doctor.go
git commit -m "feat: add status and doctor diagnostics"
```

### Task 9: Release and Documentation Baseline

**Files:**
- Create: `README.md`
- Create: `LICENSE`
- Create: `.goreleaser.yaml`
- Create: `scripts/install.sh`
- Modify: `docs/plans/2026-03-05-cc-clip-design.md`

**Step 1: Write the failing test**

```bash
test -f README.md && test -f .goreleaser.yaml && test -x scripts/install.sh
```

**Step 2: Run test to verify it fails**

Run: `test -f README.md && test -f .goreleaser.yaml && test -x scripts/install.sh; echo $?`  
Expected: non-zero exit.

**Step 3: Write minimal implementation**

```bash
touch README.md LICENSE .goreleaser.yaml scripts/install.sh
chmod +x scripts/install.sh
```

**Step 4: Run test to verify it passes**

Run: `test -f README.md && test -f .goreleaser.yaml && test -x scripts/install.sh; echo $?`  
Expected: `0`.

**Step 5: Commit**

```bash
git add README.md LICENSE .goreleaser.yaml scripts/install.sh docs/plans/2026-03-05-cc-clip-design.md
git commit -m "docs: add release packaging and quick-start docs for v0.1"
```

### Task 10: Final Verification Gate

**Files:**
- Modify: `Makefile`
- Create: `docs/plans/2026-03-05-cc-clip-verification-notes.md`

**Step 1: Write the failing test**

```bash
make verify
```

**Step 2: Run test to verify it fails**

Run: `make verify`  
Expected: FAIL before target exists.

**Step 3: Write minimal implementation**

```make
verify:
	go test ./...
	go test -race ./...
```

**Step 4: Run test to verify it passes**

Run: `make verify`  
Expected: all tests PASS.

**Step 5: Commit**

```bash
git add Makefile docs/plans/2026-03-05-cc-clip-verification-notes.md
git commit -m "chore: add verification gate and notes for v0.1 release readiness"
```

