# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Project Does

cc-clip bridges your local Mac/Windows clipboard to a remote Linux server over SSH, so `Ctrl+V` image paste works in remote Claude Code and Codex CLI sessions. It uses an xclip/wl-paste shim that transparently intercepts only Claude Code's clipboard calls, an X11 selection owner bridge for Codex CLI which reads the clipboard via X11 directly, and an SSH notification bridge that forwards Claude Code hook events (stop, permission prompt, idle) back to the local machine as native notifications.

```
Claude Code path:
  Local Mac clipboard → pngpaste → HTTP daemon (127.0.0.1:18339) → SSH RemoteForward → xclip shim → Claude Code

Codex CLI path (--codex):
  Local Mac clipboard → pngpaste → HTTP daemon (127.0.0.1:18339) → SSH RemoteForward → x11-bridge → Xvfb CLIPBOARD → arboard → Codex CLI

Notification path:
  Claude Code hook → cc-clip-hook (stdin JSON) → POST /notify via tunnel → classifier → dedup → DeliveryChain → native notification
```

## Build & Test Commands

```bash
make build                          # Build binary with version from git tags
make test                           # Run all tests (go test ./... -count=1)
make vet                            # Run go vet
go test ./internal/tunnel/ -v -run TestFetchImageRoundTrip  # Single test
make release-local                  # Build for all platforms (dist/)
```

Version is injected via `-X main.version=$(VERSION)` ldflags. The `version` variable in `cmd/cc-clip/main.go` defaults to `"dev"`.

## Architecture

### Data Flow

1. **daemon** (`internal/daemon/`) — HTTP server on loopback, reads Mac clipboard via `pngpaste`, serves images at `GET /clipboard/type` and `GET /clipboard/image`. Auth via Bearer token + User-Agent whitelist.
2. **tunnel** (`internal/tunnel/`) — Client-side HTTP calls through the SSH-forwarded port. `Probe()` checks TCP connectivity. `Client.FetchImage()` downloads and saves with timestamp+random filename.
3. **shim** (`internal/shim/template.go`) — Bash script templates for xclip and wl-paste. Intercepts two specific invocation patterns Claude Code uses, fetches via curl through tunnel, falls back to real binary on any failure.
4. **connect** (`cmd/cc-clip/main.go:cmdConnect`) — Orchestrates deployment via SSH master session: detect remote arch → incremental binary upload (hash-based skip) → install shim → sync token → verify tunnel. Supports `--force`, `--token-only` flags.
5. **ssh** (`internal/shim/ssh.go`) — `SSHSession` wraps a ControlMaster SSH connection. Single passphrase prompt; all subsequent `Exec()` and `Upload()` calls reuse the master.
6. **deploy** (`internal/shim/deploy.go`) — `DeployState` tracks binary hash, version, shim status on the remote. JSON file at `~/.cache/cc-clip/deploy-state.json`. `NeedsUpload()` / `NeedsShimInstall()` enable incremental deploys.
7. **pathfix** (`internal/shim/pathfix.go`) — Auto-detects remote shell (bash/zsh/fish) and injects `~/.local/bin` PATH marker into rc file with `# cc-clip-managed` guards.
8. **service** (`internal/service/launchd.go`) — macOS launchd integration: `Install()`, `Uninstall()`, `Status()`. Generates plist for auto-start daemon.
9. **xvfb** (`internal/xvfb/`) — Manages Xvfb virtual X server on remote. `StartRemote()` auto-detects display via `-displayfd`, reuses healthy instances, writes PID/display to `~/.cache/cc-clip/codex/`. `StopRemote()` verifies PID+command before killing.
10. **x11bridge** (`internal/x11bridge/`) — Go X11 selection owner using `github.com/jezek/xgb` (pure Go, no CGo). Claims CLIPBOARD ownership on Xvfb, responds to SelectionRequest events by fetching image data on-demand from the cc-clip HTTP daemon via SSH tunnel. Supports TARGETS negotiation, direct transfer, and INCR protocol for images >256KB.

### Notification Bridge

11. **session** (`internal/session/`) — Ring-buffer session store tracking last 5 image transfers per session ID. `AnalyzeAndRecord()` atomically assigns sequence numbers and detects duplicates by fingerprint. TTL-based cleanup via `RunCleanup()`.
12. **classifier** (`internal/daemon/classifier.go`) — `ClassifyHookPayload()` translates Claude Code hook JSON (notification, stop, etc.) into a unified `NotifyEnvelope`. Maps hook types to urgency levels: `permission_prompt`=2, `idle_prompt`=1, `stop_at_end_of_turn`=0.
13. **envelope** (`internal/daemon/envelope.go`) — Unified notification model. Three kinds: `KindImageTransfer`, `KindToolAttention`, `KindGenericMessage`. Each carries kind-specific payload structs.
14. **dedup** (`internal/daemon/dedup.go`) — Deduplicates notifications by fingerprint within a session using the session store's ring buffer.
15. **deliver** (`internal/daemon/deliver.go`) — `DeliveryChain` tries adapters in priority order (cmux → platform-native). First success stops the chain. `BuildDeliveryChain()` constructs the default chain. Also implements `Notifier` interface for backward compat.
16. **deliver_cmux** (`internal/daemon/deliver_cmux.go`) — Cross-platform tmux `display-message` adapter. Falls through if not in tmux.
17. **notify_darwin** (`internal/daemon/notify_darwin.go`) — macOS-specific: terminal-notifier or osascript fallback.
18. **claude wrapper** (`internal/shim/claude_wrapper.go`) — Bash script installed to `~/.local/bin/claude` on remote. Auto-injects `--settings` with Stop and Notification hooks when tunnel is alive. Falls through to real claude binary when tunnel is down.
19. **cc-clip-hook** (`internal/shim/hook_template.go`) — Bash script installed to `~/.local/bin/cc-clip-hook` on remote. Reads hook JSON from stdin, injects hostname, POSTs to `/notify` endpoint with nonce auth. Logs failures to `~/.cache/cc-clip/notify-health.log`.

### Key Design Decisions

- **Shim is a bash script, not a binary** — installed to `~/.local/bin/` with PATH priority over `/usr/bin/xclip`. Uses `which -a` to find the real binary, skipping its own directory.
- **Token is the daemon's token** — `cc-clip serve` generates a single token; `connect` reads it from the file and sends it to remote. Never generate a second token.
- **Binary-safe image transfer** in shim — `_cc_clip_fetch_binary()` uses `mktemp` + `curl -o tmpfile` + `cat tmpfile`, not shell variables (which strip NUL bytes) or `exec curl` (which prevents fallback). After curl succeeds, `[ ! -s "$tmpfile" ]` guards against empty responses (e.g., HTTP 204), returning exit code 10 to trigger fallback instead of outputting empty data.
- **Server-side empty guard** — `handleClipboardImage` checks `len(data) == 0` after `ImageBytes()` and returns 204, preventing 200 with empty body even if the clipboard reader returns empty data without error.
- **Exit codes are segmented** (`internal/exitcode/`) — 0 success, 10-13 business errors (no image, tunnel down, bad token, download failed), 20+ internal. Business codes trigger transparent fallback in the shim.
- **Platform clipboard** — `clipboard_darwin.go` (pngpaste), `clipboard_linux.go` (xclip/wl-paste), `clipboard_windows.go` (PowerShell). Windows uses SCP upload workflow with system tray icon and global hotkey (`Ctrl+Alt+V`).
- **Codex uses X11, not shims** — Codex CLI uses `arboard` (Rust crate) which accesses X11 CLIPBOARD directly in-process. Cannot be shimmed. Solution: Xvfb + Go X11 selection owner that claims CLIPBOARD and serves images on-demand.
- **On-demand fetch in x11-bridge** — No polling or caching. Image data is fetched from the cc-clip daemon only when a SelectionRequest arrives. Always fresh.
- **Token per-request in x11-bridge** — Token is read from file on every HTTP request, enabling `--token-only` rotation without restarting the bridge.
- **DISPLAY injection is file-driven** — The DISPLAY marker block in shell rc reads from `~/.cache/cc-clip/codex/display` at shell startup, not a hardcoded value. This supports `-displayfd` dynamic allocation.
- **Notification uses nonce auth, not session token** — `/notify` endpoint authenticates with a separate nonce (stored at `~/.cache/cc-clip/notify.nonce`), not the clipboard Bearer token. This allows independent rotation.
- **Claude wrapper is conditional** — Only injects `--settings` hooks when the cc-clip tunnel is reachable (health check). When the tunnel is down, passes through transparently so Claude Code still works normally.
- **DeliveryChain fallthrough** — Notification adapters are tried in priority order (cmux → platform-native). First success stops the chain. If all fail, the last error is returned but the hook script always exits 0 (non-blocking).
- **Hook script is fire-and-forget** — `cc-clip-hook` always exits 0 to avoid blocking Claude Code. Failures are logged to a health file, not propagated.

### Token Lifecycle

`token.Manager` holds the session in memory. `LoadOrGenerate(ttl)` reuses an unexpired token from disk, or generates a new one. Token file at `~/.cache/cc-clip/session.token` (chmod 600) stores `token\nexpires_at_rfc3339`. `ReadTokenFileWithExpiry()` returns both token and expiry. `token.TokenDirOverride` exists for test isolation — tests set it to `t.TempDir()` to avoid polluting the real cache directory. `--rotate-token` flag forces new token generation ignoring existing.

### Test Patterns

- `internal/daemon/server_test.go` uses a mock `ClipboardReader` — no real clipboard access needed.
- `internal/tunnel/fetch_test.go` uses `newIPv4TestServer(t, handler)` which forces IPv4 binding and calls `t.Skipf` (not panic) if binding fails in restricted environments.
- `internal/shim/install_test.go` uses temp directories to test shim installation without touching real PATH.
- `internal/xvfb/xvfb_test.go` uses `requireXvfb` skip guard — integration tests skip on macOS (no Xvfb available).
- `internal/x11bridge/bridge_test.go` uses `requireXvfbAndXclip` skip guard — E2E smoke test runs mock HTTP + Xvfb + x11-bridge + xclip roundtrip.
- `internal/daemon/classifier_test.go` — Tests hook JSON classification into envelopes for each hook type (notification, stop, unknown).
- `internal/daemon/dedup_test.go` — Tests duplicate detection in the ring buffer across sessions.
- `internal/daemon/deliver_test.go` — Tests DeliveryChain fallthrough behavior with mock adapters.
- `internal/shim/claude_wrapper_test.go` — Validates wrapper script port substitution.
- `internal/shim/hook_template_test.go` — Validates hook script port substitution.
- `internal/session/session_test.go` — Tests ring-buffer wrap-around and TTL cleanup.

### Shim Interception Patterns

The shim only intercepts these exact Claude Code invocations:
- xclip: `*"-selection clipboard"*"-t TARGETS"*"-o"*` and `*"-selection clipboard"*"-t image/"*"-o"*`
- wl-paste: `*"--list-types"*` and `*"--type"*"image/"*`

Everything else passes through to the real binary via `exec`.

## Cross-Architecture Binary Delivery

When `connect` detects a different remote arch (e.g., Mac arm64 → Linux amd64), it tries in order:
1. Download matching binary from GitHub Releases (needs non-`dev` version)
2. Cross-compile locally (needs Go toolchain + source)
3. Fail with actionable `--local-bin` instruction

## Known Pitfalls

- **SSH ControlMaster + RemoteForward**: If the user has `ControlMaster auto` globally, a pre-existing master connection without `RemoteForward` will be reused. The tunnel silently fails. Fix: set `ControlMaster no` and `ControlPath none` on hosts that need `RemoteForward`.
- **Token rotation on daemon restart**: Mitigated by token persistence — `LoadOrGenerate` reuses unexpired tokens. Use `cc-clip connect <host> --token-only` if only the token changed.
- **Empty image race condition**: The clipboard can change between the TARGETS check (returns "image") and the image fetch (returns 204 No Content). `curl -sf` treats 204 as success → shim outputs empty bytes → Claude Code API rejects empty base64. Guarded by `[ ! -s "$tmpfile" ]` check in `_cc_clip_fetch_binary()`.
- **Remote xclip must exist**: The shim hardcodes the real xclip path at install time. If xclip is not installed on the remote, the shim fallback fails with "No such file or directory".
- **`~/.local/bin` PATH priority**: The shim only works if `~/.local/bin` comes before `/usr/bin` in PATH. Non-interactive SSH commands may not source `.bashrc`, so the `connect` command's `which xclip` check can show the wrong result. Interactive shells (where Claude Code runs) typically source `.bashrc` correctly.
- **Xvfb display collision**: `-displayfd` avoids hardcoded `:99` collisions. If `Xvfb` is not installed on the remote, `connect --codex` fails at step 8 (preflight) with an actionable error.
- **x11-bridge survives SSH session exit**: Launched with `nohup ... < /dev/null &`. PID file at `~/.cache/cc-clip/codex/bridge.pid`. Next `connect --codex` reuses if healthy, restarts if binary was updated.
- **DISPLAY marker vs PATH marker**: Independent lifecycles. `uninstall --codex` removes DISPLAY marker only. `uninstall` (without `--codex`) removes PATH marker only. They use separate `# cc-clip-managed` guard blocks.

## Files That Need Coordinated Changes

- Adding a new API endpoint: `daemon/server.go` (handler) + `tunnel/fetch.go` (client method) + `shim/template.go` (bash interception pattern)
- Changing token format: `token/token.go` + `shim/connect.go:WriteRemoteToken` + shim templates (`_cc_clip_read_token`)
- Adding a new exit code: `exitcode/exitcode.go` + `cmd/cc-clip/main.go:classifyError` + shim templates (return codes)
- Changing Codex deploy flow: `cmd/cc-clip/main.go:runConnectCodex` + `xvfb/xvfb.go` + `x11bridge/bridge.go` + `shim/pathfix.go` (DISPLAY marker)
- Adding a new notification kind: `daemon/envelope.go` (NotifyKind + payload struct) + `daemon/classifier.go` (hook→envelope mapping) + `daemon/deliver.go` (formatNotification display text)
- Changing hook injection: `shim/claude_wrapper.go` (wrapper template) + `shim/hook_template.go` (hook script) + `shim/connect.go` (deploy steps)
- Adding a notification adapter: implement `Deliverer` interface + register in `daemon/deliver.go:BuildDeliveryChain()`
