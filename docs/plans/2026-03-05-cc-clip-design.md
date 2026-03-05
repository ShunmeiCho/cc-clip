# cc-clip Design (2026-03-05)

## 1. Goal

Enable image paste into remote Claude Code sessions over SSH with near-native `Ctrl+V` behavior.

- GA target: `Mac -> Linux`
- Beta target: `Windows -> Linux`

## 2. Problem

In SSH workflows, remote Claude Code cannot access the local machine clipboard directly.  
Users can copy screenshots locally, but remote `Ctrl+V` image paste fails or reads the wrong clipboard.

## 3. Options Considered

1. Terminal-level keybinding interception (`Ctrl+V` hook per terminal)
- Pros: works without relying on remote clipboard tools
- Cons: terminal-specific integrations, higher maintenance

2. Claude protocol emulation (reproduce private image-paste payload)
- Pros: potentially closest to native behavior
- Cons: fragile, version-sensitive, high reverse-engineering cost

3. Remote clipboard-tool shim over SSH tunnel (recommended)
- Pros: terminal-agnostic, transparent to Claude Code, stable contract
- Cons: requires remote helper installation

## 4. Selected Architecture

Primary path: `xclip/wl-paste shim` on remote host, backed by local clipboard daemon via SSH `RemoteForward`.

Fallback path: explicit terminal helper command (`cc-clip send`) for environments where shim cannot be installed.

### 4.1 High-Level Flow

1. User presses `Ctrl+V` in remote Claude Code.
2. Claude Code calls remote `xclip`/`wl-paste`.
3. User-space shim intercepts only Claude-required argument patterns.
4. Shim fetches clipboard image bytes through local tunnel endpoint.
5. Claude Code receives image bytes and proceeds as native flow.
6. On non-fatal failures, shim transparently falls back to real system binary.

## 5. Components

## 5.1 Local Daemon (`cc-clip serve`)

- Language: Go (single binary)
- Listen address: `127.0.0.1`
- Default port: `18339` (configurable)
- API:
  - `GET /health`
  - `GET /clipboard/type` => `image|text|empty`
  - `GET /clipboard/image` => `image/png` bytes, `204` if no image
- Auth: bearer session token required for all endpoints

## 5.2 Remote Shim

- Install mode (default): `PATH` precedence (`~/.local/bin/xclip`, `~/.local/bin/wl-paste`)
- System overwrite/rename mode: optional `--system`, not default
- Behavior:
  - Intercept only known Claude invocation subset
  - Delegate all other calls to real binary
  - Fast timeout + safe fallback

## 5.3 Session Bootstrap (`cc-clip connect <host>`)

1. Ensure local daemon available.
2. Ensure/guide SSH forward config.
3. Detect remote arch.
4. Upload `cc-clip` binary to remote user bin.
5. Install shim in user bin.
6. Generate short-lived session token and write remote token file (`chmod 600`).
7. Validate end-to-end path with probe.

## 6. Security Model

- Local service bound to loopback only.
- Session-scoped token with TTL (default 8-12h).
- Remote token stored in user-private file (`0600`).
- Max image size cap: `20MB`.
- Timeout controls:
  - probe: `500ms`
  - fetch: `5s`
  - total shim execution hard limit: `8s`
- Optional request-source checks (lightweight User-Agent/Origin gating).

## 7. Error Handling and Exit Codes

Business/non-fatal domain (shim may fallback):

- `0`: success
- `10`: no image in clipboard
- `11`: tunnel unreachable
- `12`: token invalid/expired
- `13`: download/transfer failed

Fatal/internal domain:

- `20+`: internal error (still fallback, but emit strong stderr diagnostics)

Fallback policy:

- For `10/11/12/13`, transparently delegate to real clipboard binary.
- For `20+`, delegate but emit explicit diagnostic output.

## 8. CLI Surface (MVP)

- `cc-clip serve`
- `cc-clip connect <host>`
- `cc-clip install`
- `cc-clip uninstall`
- `cc-clip paste` (manual fallback)
- `cc-clip status`
- `cc-clip doctor`
- `cc-clip doctor --host <host>`
- `cc-clip version`

## 9. Installation and Distribution

MVP distribution:

1. GitHub Releases binaries
2. `install.sh` bootstrap script
3. `cc-clip push/connect` remote deployment flow

Deferred to `v0.2`: Homebrew formula.

## 10. Repository Structure (Proposed)

```text
cc-clip/
├── cmd/cc-clip/main.go
├── internal/
│   ├── daemon/
│   ├── shim/
│   ├── tunnel/
│   ├── token/
│   └── doctor/
├── scripts/
├── docs/
│   ├── plans/
│   ├── ARCHITECTURE.md
│   └── SECURITY.md
├── .goreleaser.yaml
├── go.mod
├── LICENSE
├── README.md
└── Makefile
```

## 11. MVP Scope (v0.1)

Included:

- Mac local daemon + token auth
- Remote xclip shim primary path
- `connect` bootstrap flow
- `doctor` local and end-to-end checks
- GitHub Releases + install script

Deferred:

- wl-paste parity hardening
- Windows daemon beta
- automatic multi-session dynamic porting
- autostart agents
- config file support
- terminal interception fallback presets

## 12. Milestones

- Week 1: daemon + API + token lifecycle
- Week 2: shim + install/uninstall
- Week 3: connect + doctor + e2e validation
- Week 4: packaging, docs, v0.1.0 release

## 13. Acceptance Criteria

- Remote Claude Code receives pasted image with no user-visible workflow change under supported setup.
- Shim failures do not block normal clipboard behavior.
- `doctor --host` can identify all major misconfigurations in one run.
- No root/sudo required in default path.

