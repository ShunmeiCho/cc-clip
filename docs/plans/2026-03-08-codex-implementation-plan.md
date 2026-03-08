# Codex Support — Implementation Plan

**Design:** `2026-03-08-codex-support-design.md`
**Branch:** `feat/codex-support`

## Phase Overview

| Phase | Scope | Dependencies | Deliverable |
|-------|-------|-------------|-------------|
| 1 | DeployState + DISPLAY marker | None | Extended state + pathfix |
| 2 | Xvfb lifecycle management | None | `internal/xvfb/` package |
| 3 | X11 Selection Owner | Phase 2 (for integration tests) | `internal/x11bridge/` package |
| 4 | `connect --codex` + `uninstall --codex` | Phases 1-3 | CLI integration |
| 5 | CI + documentation | Phase 4 | CI config, README, CLAUDE.md |

Phases 1 and 2 are independent and can be implemented in parallel.

---

## Phase 1: DeployState Extension + DISPLAY Marker

### 1.1 Extend DeployState struct

- [ ] Add `CodexDeployState` struct to `internal/shim/deploy.go`
  ```go
  type CodexDeployState struct {
      Enabled      bool   `json:"enabled"`
      Mode         string `json:"mode"`
      DisplayFixed bool   `json:"display_fixed"`
  }
  ```
- [ ] Add `Codex *CodexDeployState` field to `DeployState` with `json:"codex,omitempty"`
- [ ] Add `NeedsCodexSetup(remote *DeployState) bool` helper

### 1.2 Extend DeployState tests

- [ ] Test: marshal with `Codex: nil` -> no `codex` key in JSON
- [ ] Test: marshal with `Codex` populated -> `codex` block present
- [ ] Test: unmarshal old JSON (no `codex` field) -> `Codex: nil`, no error
- [ ] Test: `NeedsCodexSetup` returns true when nil, false when enabled

### 1.3 DISPLAY marker block

- [ ] Add constants to `internal/shim/pathfix.go`:
  - `displayMarkerStart = "# >>> cc-clip Codex DISPLAY (do not edit) >>>"`
  - `displayMarkerEnd = "# <<< cc-clip Codex DISPLAY (do not edit) <<<"`
  - `displayBlock()` function returning the full marker block
- [ ] Add `IsDisplayFixedSession(session RemoteExecutor) (bool, error)`
- [ ] Add `FixDisplaySession(session RemoteExecutor) error` (prepend to rc, same as PATH)
- [ ] Add `RemoveDisplayMarkerSession(session RemoteExecutor) error`

### 1.4 DISPLAY marker tests

- [ ] Test: `displayBlock()` content matches design spec
- [ ] Test: inject DISPLAY marker independently of PATH marker
- [ ] Test: inject is idempotent (double inject -> single block)
- [ ] Test: remove DISPLAY marker without affecting PATH marker
- [ ] Test: remove is idempotent (double remove -> no error)

### Phase 1 commit
```
feat: extend DeployState with codex block and DISPLAY marker
```

---

## Phase 2: Xvfb Lifecycle Management

### 2.1 Create `internal/xvfb/` package

- [ ] Create `internal/xvfb/xvfb.go`
- [ ] Define types:
  ```go
  type State struct {
      Display string // e.g. "42"
      PID     int
  }
  ```
- [ ] Implement `ParseDisplayFile(path string) (string, error)` — parse display file content
- [ ] Implement `SocketPath(display string) string` — `/tmp/.X11-unix/X<n>`
- [ ] Implement `IsHealthy(stateDir string) (*State, bool)` — check PID alive + socket exists
- [ ] Implement `CleanStale(stateDir string) error` — remove stale pid/display files

### 2.2 Remote Xvfb start/stop (via SSH session)

- [ ] Implement `StartRemote(session RemoteExecutor, stateDir string) (*State, error)`:
  1. Check `Xvfb` in PATH (fail with install hint if missing)
  2. Check existing health -> reuse if healthy
  3. Clean stale state
  4. Start via nohup + `-displayfd` (shell script from design 6a)
  5. Wait for display file (5 retries, 200ms interval)
  6. Return `State{Display, PID}`
- [ ] Implement `StopRemote(session RemoteExecutor, stateDir string) error`:
  1. Read PID, verify command matches `Xvfb`
  2. TERM -> wait -> KILL if needed
  3. Clean state files
- [ ] Implement `CheckAvailable(session RemoteExecutor) error` — `which Xvfb`

### 2.3 Xvfb tests

- [ ] Unit test: `ParseDisplayFile` — "99", ":99", "", "abc", "99\n"
- [ ] Unit test: `SocketPath` — "99" -> `/tmp/.X11-unix/X99`
- [ ] Integration test (requireXvfb): `StartRemote` -> verify display file, PID, socket
- [ ] Integration test (requireXvfb): `StopRemote` -> verify cleanup
- [ ] Integration test (requireXvfb): reuse healthy -> PID unchanged
- [ ] Integration test (requireXvfb): recover stale -> new PID

### Phase 2 commit
```
feat: add Xvfb lifecycle management package
```

---

## Phase 3: X11 Selection Owner (x11-bridge)

### 3.1 Atom management

- [ ] Create `internal/x11bridge/atoms.go`
- [ ] Define `AtomCache` struct with lazy intern:
  ```go
  type AtomCache struct {
      conn     *xgb.Conn
      cache    map[string]xproto.Atom
  }
  func (a *AtomCache) Get(name string) (xproto.Atom, error)
  ```
- [ ] Pre-define atom names: `CLIPBOARD`, `TARGETS`, `TIMESTAMP`, `MULTIPLE`, `INCR`, `image/png`

### 3.2 Selection request handling

- [ ] Create `internal/x11bridge/selection.go`
- [ ] Implement `handleTargets(event, atomCache, httpClient)`:
  1. Probe tunnel / GET /clipboard/type
  2. If image -> respond with [TARGETS, TIMESTAMP, image/png]
  3. If not image -> respond Property=None
- [ ] Implement `handleImageRequest(event, atomCache, httpClient)`:
  1. GET /clipboard/image
  2. If <=maxDirectSize -> ChangeProperty direct
  3. If >maxDirectSize -> start INCR
  4. Send SelectionNotify
- [ ] Implement `refuseRequest(event)` — send SelectionNotify with Property=None
- [ ] Implement `sendSelectionNotify(conn, event, property)`

### 3.3 INCR protocol

- [ ] Create `internal/x11bridge/incr.go`
- [ ] Implement `IncrTransfer` struct:
  ```go
  type IncrTransfer struct {
      data      []byte
      offset    int
      chunkSize int
      property  xproto.Atom
      requestor xproto.Window
  }
  ```
- [ ] Implement `startIncr(conn, event, data, atomCache)` — write INCR marker
- [ ] Implement `handlePropertyDelete(conn, transfer)` — write next chunk or empty (done)
- [ ] Implement `isComplete(transfer)` — offset >= len(data)

### 3.4 Bridge main loop

- [ ] Create `internal/x11bridge/bridge.go`
- [ ] Define `Bridge` struct:
  ```go
  type Bridge struct {
      display    string
      port       int
      tokenFile  string
      conn       *xgb.Conn
      window     xproto.Window
      atoms      *AtomCache
      activeIncr *IncrTransfer  // nil when no INCR in progress
  }
  ```
- [ ] Implement `New(display, port, tokenFile string) (*Bridge, error)`:
  1. Connect to X display
  2. Create invisible window
  3. Initialize AtomCache
  4. Claim CLIPBOARD ownership
- [ ] Implement `Run(ctx context.Context) error` — main event loop:
  - `SelectionRequest` -> dispatch to handleTargets / handleImageRequest
  - `SelectionClear` -> reclaim ownership
  - `PropertyNotify` (delete) -> if active INCR, handle chunk
  - Unknown events -> ignore
- [ ] Implement `readToken() (string, error)` — read from tokenFile each time
- [ ] Implement `fetchClipboardType() (*ClipboardInfo, error)` — HTTP GET /clipboard/type
- [ ] Implement `fetchClipboardImage() ([]byte, error)` — HTTP GET /clipboard/image

### 3.5 x11-bridge subcommand

- [ ] Add `case "x11-bridge"` to `cmd/cc-clip/main.go:main()`
- [ ] Implement `cmdX11Bridge()`:
  - Parse `--display` and `--port` flags
  - Create Bridge, run with signal handling (SIGTERM -> graceful shutdown)
  - Log structured one-line entries

### 3.6 Add xgb dependency

- [ ] `go get github.com/jezek/xgb`

### 3.7 X11 bridge tests

- [ ] Unit test: `AtomCache` caches intern results
- [ ] Unit test: `IncrTransfer` chunk iteration — correct offsets, final empty chunk
- [ ] Integration test (requireXvfb + xclip): `TestBridge_ClaimOwnership`
- [ ] Integration test: `TestBridge_TargetsResponse` (mock HTTP, image)
- [ ] Integration test: `TestBridge_TargetsNoImage` (mock HTTP, text)
- [ ] Integration test: `TestBridge_ImageSmall` (<256KB, direct)
- [ ] Integration test: `TestBridge_ImageLargeINCR` (>256KB, INCR)
- [ ] Integration test: `TestBridge_TunnelDown` (no HTTP server)
- [ ] Integration test: `TestBridge_TokenInvalid` (401)
- [ ] Integration test: `TestBridge_EmptyImage204`
- [ ] Integration test: `TestBridge_SelectionClearRecovery`
- [ ] E2E test: `TestE2E_FullPasteFlow` (mock HTTP + Xvfb + xclip roundtrip)

### Phase 3 commit
```
feat: add X11 clipboard selection owner (x11-bridge)
```

---

## Phase 4: CLI Integration

### 4.1 `connect --codex`

- [ ] Add `--codex` flag parsing to `connectOpts` struct in `cmd/cc-clip/main.go`
- [ ] Add `codex bool` field to `connectOpts`
- [ ] In `runConnect()`, after step 6 (token sync), add codex branch:
  ```go
  if opts.codex {
      runConnectCodex(session, opts, newState)
  }
  ```
- [ ] Implement `runConnectCodex(session, opts, newState)`:
  - Step 7: Codex preflight — `xvfb.CheckAvailable(session)`
  - Step 8: `xvfb.StartRemote(session, stateDir)` or reuse
  - Step 9: Start/restart x11-bridge
    - If `needsUpload` was true -> unconditionally restart bridge
    - Else -> check bridge health, reuse or start
  - Step 10: `FixDisplaySession(session)`, verify, update state
- [ ] Bridge start via SSH:
  ```go
  func startBridgeRemote(session, display, port, stateDir) error
  func stopBridgeRemote(session, stateDir) error
  func isBridgeHealthy(session, stateDir) bool
  ```
- [ ] Update `newState.Codex` on success
- [ ] On codex failure: log error, print "Claude shim is ready, but Codex support failed", return non-zero

### 4.2 `--force --codex` and `--token-only --codex`

- [ ] `--force --codex`: set `remoteState.Codex = nil` to force all codex steps
- [ ] `--token-only --codex`: skip codex steps entirely (bridge reads token from file)

### 4.3 `setup --codex`

- [ ] Pass `--codex` through to `runConnect()` in `cmdSetup()`

### 4.4 `uninstall --codex`

- [ ] Extend `cmdUninstall()` to detect `--codex` flag
- [ ] If `--codex` and `--host`:
  1. SSH session to host
  2. `stopBridgeRemote(session, stateDir)`
  3. `xvfb.StopRemote(session, stateDir)`
  4. `session.Exec("rm -rf ~/.cache/cc-clip/codex/")`
  5. `RemoveDisplayMarkerSession(session)`
  6. Read deploy state, set `Codex = nil`, write back
  7. Verify and output
- [ ] If `--codex` without `--host` (local machine):
  1. Same steps but using local exec instead of SSH
- [ ] Fallback if remote binary missing: shell-based cleanup

### 4.5 `doctor --host` codex diagnostics

- [ ] If `deployState.Codex != nil && deployState.Codex.Enabled`:
  - Check Xvfb health (PID + socket)
  - Check bridge health (PID)
  - Check DISPLAY marker in rc
  - Report status for each
- [ ] If `deployState.Codex == nil`:
  - Report "Codex: not configured"
- [ ] If user has DISPLAY set but broken:
  - Suggest `unset DISPLAY`

### 4.6 Update `printUsage()` help text

- [ ] Add `--codex` to connect/setup/uninstall descriptions
- [ ] Add `x11-bridge` as internal command (brief mention)

### 4.7 CLI integration tests

- [ ] Test: flag parsing — `--codex`, `--force --codex`, `--token-only --codex`
- [ ] Test: `--token-only --codex` skips codex steps
- [ ] Test: uninstall `--codex` cleanup sequence (mock SSH session)

### Phase 4 commit
```
feat: integrate --codex flag into connect/setup/uninstall
```

---

## Phase 5: CI + Documentation

### 5.1 CI configuration

- [ ] Add `xvfb` and `xclip` to GitHub Actions test job:
  ```yaml
  - name: Install X11 test dependencies
    run: sudo apt-get install -y xvfb xclip
  ```

### 5.2 Update CLAUDE.md

- [ ] Add x11-bridge to Architecture section
- [ ] Add `internal/x11bridge/` and `internal/xvfb/` to file structure
- [ ] Add Codex-related coordinated changes
- [ ] Add `--codex` to Known Pitfalls if applicable

### 5.3 Update README

- [ ] Add Codex support section
- [ ] Usage: `cc-clip connect <host> --codex`
- [ ] Prerequisites: `Xvfb` on remote
- [ ] Architecture diagram update

### Phase 5 commit
```
docs: add Codex support to CI, CLAUDE.md, and README
```

---

## Implementation Order Summary

```
Phase 1 ──┐
           ├──> Phase 3 ──> Phase 4 ──> Phase 5
Phase 2 ──┘
```

Phases 1 and 2 can run in parallel (no dependencies).
Phase 3 depends on Phase 2 (for Xvfb in integration tests).
Phase 4 depends on all prior phases.
Phase 5 depends on Phase 4.

Total: ~45 checklist items across 5 phases.
