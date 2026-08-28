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
  Claude Code hook → cc-clip plugin run claude-notify (stdin JSON) → POST /notify via tunnel → classifier → dedup → DeliveryChain → native notification
  (fallback when the settings.json merge fails: Claude Code hook → cc-clip-hook → same POST /notify)
```

## Build & Test Commands

```bash
make build                          # Build binary with version from git tags
make test                           # Run all tests (go test ./... -count=1)
make vet                            # Run go vet
go test ./internal/tunnel/ -v -run TestFetchImageRoundTrip  # Single test
make release-local                  # Build bare binaries for all platforms (dist/), local testing only
```

Version is injected via `-X main.version=$(VERSION)` ldflags. The `version` variable in `cmd/cc-clip/main.go` defaults to `"dev"`.

### Release Process

Production releases use **goreleaser** via GitHub Actions (`.github/workflows/release.yml`). Push a version tag to trigger:

```bash
git tag v0.6.0
git push origin v0.6.0
# CI runs: test → contract check → goreleaser → published release with tar.gz + checksums
```

**NEVER release manually with `make release-local` + `gh release create`.** The install script (`scripts/install.sh`) expects goreleaser's naming convention (`cc-clip_{version}_{os}_{arch}.tar.gz`). Bare binaries from `make release-local` use a different naming scheme (`cc-clip-{os}-{arch}`) and will cause install script 404s. `make release-local` is for local cross-compilation testing only.

goreleaser config: `.goreleaser.yaml`. Release is published automatically (not draft).

## Architecture

### Data Flow

1. **daemon** (`internal/daemon/`) — HTTP server on loopback, reads Mac clipboard via `pngpaste`, serves images at `GET /clipboard/type` and `GET /clipboard/image`. Also accepts reverse text copies at `POST /clipboard/text` (`cc-clip copy` on the remote → local clipboard, #128): bytes are written verbatim via the platform writer (`clipwrite_{darwin,linux,windows}.go`), and an accepted write emits a calm notification whose text is deliberately CONSTANT — no byte count, no content — so that a burst of writes (every neovim yank is one write on the transparent shim path) collapses into a single notification through the dedup window, while at least one still fires per window so clipboard poisoning stays visible. Auth via Bearer token + User-Agent whitelist.
2. **tunnel** (`internal/tunnel/`) — Client-side HTTP calls through the SSH-forwarded port. `Probe()` checks TCP connectivity. `Client.FetchImage()` downloads and saves with timestamp+random filename.
3. **shim** (`internal/shim/template.go`) — Bash script templates for xclip and wl-paste. Intercepts two specific invocation patterns Claude Code uses, fetches via curl through tunnel, falls back to real binary on any failure.
4. **connect** (`cmd/cc-clip/connect.go:cmdConnect`) — Orchestrates deployment via SSH master session: detect remote arch → incremental binary upload (hash-based skip) → install shim → sync token → verify tunnel. Supports `--force`, `--token-only` flags.
5. **ssh** (`internal/shim/ssh.go`) — `SSHSession` wraps a ControlMaster SSH connection. Single passphrase prompt; all subsequent `Exec()` and `Upload()` calls reuse the master.
6. **deploy** (`internal/shim/deploy.go`) — `DeployState` tracks binary hash, version, shim status on the remote. JSON file at `~/.cache/cc-clip/deploy.json`. `NeedsUpload()` / `NeedsShimInstall()` enable incremental deploys.
7. **pathfix** (`internal/shim/pathfix.go`) — Auto-detects remote shell (bash/zsh/fish) and injects `~/.local/bin` PATH marker into rc file with `# cc-clip-managed` guards.
8. **service** (`internal/service/launchd.go`) — macOS launchd integration: `Install()`, `Uninstall()`, `Status()`. Generates plist for auto-start daemon.
9. **xvfb** (`internal/xvfb/`) — Manages Xvfb virtual X server on remote. `StartRemote()` auto-detects display via `-displayfd`, reuses healthy instances, writes PID/display to `~/.cache/cc-clip/codex/`. `StopRemote()` verifies PID+command before killing.
10. **x11bridge** (`internal/x11bridge/`) — Go X11 selection owner using `github.com/jezek/xgb` (pure Go, no CGo). Claims CLIPBOARD ownership on Xvfb, responds to SelectionRequest events by fetching image data on-demand from the cc-clip HTTP daemon via SSH tunnel. Supports TARGETS negotiation, direct transfer, and INCR protocol for images >256KB; stalled INCR transfers are cleared after a 30s inactivity timeout so future paste requests are not refused forever.

### Notification Bridge

11. **session** (`internal/session/`) — Ring-buffer session store tracking last 5 image transfers per session ID. `AnalyzeAndRecord()` atomically assigns sequence numbers and detects duplicates by fingerprint. TTL-based cleanup via `RunCleanup()`.
12. **classifier** (`internal/daemon/classifier.go`) — `ClassifyHookPayload()` translates Claude Code hook JSON (notification, stop, etc.) into a unified `NotifyEnvelope`. Maps hook types to urgency levels: `permission_prompt`=2, `idle_prompt`=1, `stop_at_end_of_turn`=0.
13. **envelope** (`internal/daemon/envelope.go`) — Unified notification model. Three kinds: `KindImageTransfer`, `KindToolAttention`, `KindGenericMessage`. Each carries kind-specific payload structs.
14. **dedup** (`internal/daemon/dedup.go`) — Deduplicates notifications by fingerprint within a session using the session store's ring buffer.
15. **deliver** (`internal/daemon/deliver.go`) — `DeliveryChain` tries adapters in priority order (cmux → platform-native). First success stops the chain. `BuildDeliveryChain()` constructs the default chain. Also implements `Notifier` interface for backward compat.
16. **deliver_cmux** (`internal/daemon/deliver_cmux.go`) — Cross-platform tmux `display-message` adapter. Falls through if not in tmux.
17. **notify_darwin** (`internal/daemon/notify_darwin.go`) — macOS-specific: terminal-notifier or osascript fallback.
18. **claude settings hooks** (`internal/shim/settings.go`) — `connect` merges managed Stop and Notification hooks into remote `~/.claude/settings.json` and cleans up legacy wrappers to avoid double delivery. The `~/.local/bin/claude` wrapper remains only as a fallback when settings merge fails.
19. **cc-clip-hook** (`internal/shim/hook_template.go`) — Bash script installed to `~/.local/bin/cc-clip-hook` on remote. Reads hook JSON from stdin, injects hostname, POSTs to `/notify` endpoint with nonce auth. Logs failures to `~/.cache/cc-clip/notify-health.log`. This is the **fallback** path; the managed settings.json hook runs the plugin runner below instead.
20. **plugin runner** (`internal/plugin/`) — In-process notify adapters behind `cc-clip plugin run <name>` (`claude-notify` | `codex-notify` | `agy-notify` | `opencode-notify`). `Run()` reads the tool's hook JSON from stdin and `PostNotification()` POSTs it to `/notify` with nonce auth. This is what `settings.go`'s managed hook command (`env CC_CLIP_MANAGED=1 cc-clip plugin run claude-notify`) executes, so the remote binary — not the bash hook script — carries the payload. Note this is an internal Go dispatcher, **not** a Claude Code plugin (`.claude-plugin/` + `hooks/hooks.json`), which does not exist in this repo.

### Key Design Decisions

- **Shim is a bash script, not a binary** — installed to `~/.local/bin/` with PATH priority over `/usr/bin/xclip`. Uses `which -a` to find the real binary, skipping its own directory.
- **Token is the daemon's token** — `cc-clip serve` generates a single token; `connect` reads it from the file and sends it to remote. Never generate a second token.
- **Binary-safe image transfer** in shim — `_cc_clip_fetch_binary()` uses `mktemp` + `curl -o tmpfile` + `cat tmpfile`, not shell variables (which strip NUL bytes) or `exec curl` (which prevents fallback). After curl succeeds, `[ ! -s "$tmpfile" ]` guards against empty responses (e.g., HTTP 204), returning exit code 10 to trigger fallback instead of outputting empty data.
- **Server-side empty guard** — `handleClipboardImage` checks `len(data) == 0` after `ImageBytes()` and returns 204, preventing 200 with empty body even if the clipboard reader returns empty data without error.
- **Exit codes are segmented** (`internal/exitcode/`) — 0 success, 10-13 business errors (no image, tunnel down, bad token, download failed), 20+ internal. Business codes trigger transparent fallback in the shim.
- **Platform clipboard** — `clipboard_darwin.go` (pngpaste), `clipboard_linux.go` (xclip/wl-paste), `clipboard_windows.go` (PowerShell). Windows uses SCP upload workflow with system tray icon and global hotkey (`Ctrl+Alt+V`).
- **Codex uses X11, not shims** — Codex CLI uses `arboard` (Rust crate) which accesses X11 CLIPBOARD directly in-process. Cannot be shimmed. Solution: Xvfb + Go X11 selection owner that claims CLIPBOARD and serves images on-demand.
- **On-demand fetch in x11-bridge** — No polling or caching. Image data is fetched from the cc-clip daemon only when a SelectionRequest arrives. Always fresh.
- **INCR transfer timeout** — x11-bridge intentionally handles one active INCR transfer at a time. If a requestor starts INCR but stops deleting the transfer property, the bridge clears that transfer after 30s of inactivity and continues serving later requests.
- **Token per-request in x11-bridge** — Token is read from file on every HTTP request, enabling `--token-only` rotation without restarting the bridge.
- **DISPLAY injection is file-driven** — The DISPLAY marker block in shell rc reads from `~/.cache/cc-clip/codex/display` at shell startup, not a hardcoded value. This supports `-displayfd` dynamic allocation.
- **Notification uses nonce auth, not session token** — `/notify` endpoint authenticates with a separate nonce (stored at `~/.cache/cc-clip/notify.nonce`), not the clipboard Bearer token. This allows independent rotation.
- **Claude hooks are settings-first** — `cc-clip connect` prefers durable `~/.claude/settings.json` managed hooks. The legacy `~/.local/bin/claude` wrapper is fallback-only because Claude Code self-update can overwrite that symlink.
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

The shim intercepts these invocation shapes (covers Claude Code, opencode, and Cursor CLI; other consumers that use the same flags get interception for free):
- xclip: `*"-selection clipboard"*"-t TARGETS"*"-o"*` and `*"-selection clipboard"*"-t image/"*"-o"*`
- wl-paste: `*"--list-types"*` (type listing — Claude) and `*"--type"*"image/"*`, `*"-t image/"*` (image read — Claude and Cursor use `--type`, opencode uses `-t`)
- xclip WRITE (#128 phase 2): `*"-selection clipboard"*` without `-o`/`-out` in argv — dual-write: the payload is POSTed to the local daemon (`/clipboard/text`) AND replayed into the real xclip, so the remote clipboard behaves exactly as before. Success if EITHER side takes the copy (headless remotes have no real xclip). `-selection primary` and unmatched `-o` read shapes fall through untouched.
- wl-copy (separate companion shim, installed with the wl-paste target): forwards plain clipboard TEXT writes the same dual-write way; `--primary`, `--clear`, non-text `--type`, `-v`/`-h` and unknown options pass through to the real binary. `-n/--trim-newline` is mirrored on the forwarded payload so both clipboards end up byte-identical.

Cursor reads via `xclip -selection clipboard -t <mime> -o` / `wl-paste --type <mime>` over image/png,jpeg,gif,webp — all four hit the existing image branches, so the transport needs no new shim code. Its gate is environmental: Cursor only attempts a clipboard read when `DISPLAY` or `WAYLAND_DISPLAY` is set in its shell, and cc-clip deliberately does not inject one (issue #109, option C). It also kills clipboard children after ~4s, hence the `CC_CLIP_FETCH_TIMEOUT_MS=3000` guidance printed by the Cursor target.

Everything else passes through to the real binary via `exec`. The `-l` short form of `--list-types` is deliberately not matched because no current consumer uses it and matching standalone `-l` without false positives is non-trivial. End-to-end coverage is asserted by `TestShimInterceptsMatchingInvocations` in `internal/shim/template_test.go`, which runs the generated bash against a mock HTTP daemon and verifies stdout contains the daemon's payload for matched patterns (proving interception took the HTTP path) and fallback captures the original args for unmatched patterns.

## Cross-Architecture Binary Delivery

When `connect` detects a different remote arch (e.g., Mac arm64 → Linux amd64), it tries in order:
1. Download matching binary from GitHub Releases (needs non-`dev` version)
2. Cross-compile locally (needs Go toolchain + source)
3. Fail with actionable `--local-bin` instruction

## Known Pitfalls

- **SSH ControlMaster + RemoteForward**: If the user has `ControlMaster auto` globally, a pre-existing master connection without `RemoteForward` will be reused. The tunnel silently fails. Fix: set `ControlMaster no` and `ControlPath none` on hosts that need `RemoteForward`.
- **Remote PATH prelude hides the user's own bin directories**: `WrapRemoteShell` prepends `remotePathPrelude` (`internal/shim/ssh.go`) to *every* remote command, and it puts `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` **ahead** of the inherited `$PATH`. That is deliberate — #106 added it so coreutils resolve on minimal images (Coder workspaces, slim containers) where non-interactive SSH has a near-empty PATH, and removing it brings back `exit 127`. The consequence is that `command -v <bin>` through `SSHSession.Exec` or `doctor`'s `remoteExecNoForward` can **never** see `~/.local/bin`, `~/.nix-profile/bin`, pipx, or asdf — a stale system copy silently wins instead. Any feature that must resolve a binary from the *user's* environment has to run it under the login shell; `resolveInInteractiveShell` (`internal/doctor/remote.go`) is the working precedent.
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
- Changing Codex deploy flow: `cmd/cc-clip/connect.go:runConnectCodex` + `xvfb/xvfb.go` + `x11bridge/bridge.go` + `shim/pathfix.go` (DISPLAY marker)
- Adding a new notification kind: `daemon/envelope.go` (NotifyKind + payload struct) + `daemon/classifier.go` (hook→envelope mapping) + `daemon/deliver.go` (formatNotification display text)
- Changing hook injection: `shim/settings.go` (managed `~/.claude/settings.json` merge — the durable path) + `cmd/cc-clip/notify.go` (deploy steps N3/N4) + `shim/hook_template.go` (fallback hook script) + `shim/claude_wrapper.go` (fallback wrapper template)
- Adding a notification adapter: implement `Deliverer` interface + register in `daemon/deliver.go:BuildDeliveryChain()`
- Changing release asset format: `.goreleaser.yaml` (archive naming/format) + `scripts/install.sh` (download URL + extraction logic) — these MUST stay in sync
