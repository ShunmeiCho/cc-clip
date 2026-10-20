# Commands Reference

Complete cc-clip command reference. For what each feature is for and what you should see when it works, see [What You Can Do](../README.md#what-you-can-do); for a one-line purpose of every command, see [All Commands](../README.md#all-commands).

## Local daemon

| Command | Description |
|---------|-------------|
| `cc-clip serve` | Start daemon in foreground |
| `cc-clip serve --rotate-token` | Start daemon with forced new token |
| `cc-clip serve --text <mode>` | Limit the text clipboard bridge: `off`, `read`, `write` or `both` (default; env `CC_CLIP_TEXT`). Disabled directions answer 403. Image paste is unaffected |
| `cc-clip service install --text off` | Install the service with a text mode. The mode is written to the service definition and kept by `setup`, `connect`, `update` and a re-run `service install`; `service uninstall` forgets it |
| `cc-clip service install` | Install local daemon service (macOS launchd / Windows logon launcher) |
| `cc-clip service uninstall` | Remove local daemon service |
| `cc-clip service status` | Show service status |
| `cc-clip status` | Show daemon, port and token status |
| `cc-clip update` | Install the latest release on this machine (macOS / Linux); then run `connect <host> --force` per host |
| `cc-clip update --check` | Only report whether a newer release exists |
| `cc-clip update --to vX.Y.Z` | Install a specific version |
| `cc-clip update --force` | Re-install even at the target version; ignore another daemon on the same port |

## Setup and deploy

> **Version note:** Per-target flags (`--all`, `--opencode`, `--agy`, `--claude`, and `--codex` as Codex-only) are available in **v0.9.0+**. On v0.8.x, the only target flag was `--codex`, and it added Codex support **on top of** the Claude shim. See [Upgrading from v0.8.x to v0.9.0](upgrading.md#upgrading-from-v08x-to-v090).

| Command | Description |
|---------|-------------|
| `cc-clip setup <host>` | Full setup: deps, SSH config, daemon, deploy (default target: Claude) |
| `cc-clip connect <host>` | Deploy to remote (incremental; default target: Claude) |
| `cc-clip connect <host> --claude` | Claude Code: clipboard shim + claude-notify (default) |
| `cc-clip connect <host> --codex` | Codex CLI **only**: Xvfb + x11-bridge + codex-notify, no Claude shim (v0.9.0 breaking; use `--all` for both) |
| `cc-clip connect <host> --opencode` | opencode: clipboard shim + opencode-notify |
| `cc-clip connect <host> --agy` | Antigravity: agy-notify (alias `--antigravity`) |
| `cc-clip connect <host> --cursor` | Cursor CLI: clipboard shim + cursor-notify (needs `DISPLAY` in Cursor's shell — see below) |
| `cc-clip connect <host> --all` | Every target (Claude + Codex + opencode + agy + Cursor) |
| `cc-clip connect <host> --token-only` | Sync token only (fast) |
| `cc-clip setup <host> --use-remote-bin` | Full setup using `cc-clip` from the remote PATH; skip binary upload |
| `cc-clip connect <host> --use-remote-bin` | Incremental deploy using `cc-clip` from the remote PATH; skip binary upload |
| `cc-clip connect <host> --auto-recover` | Recover from v0.7.0 wrapper corruption + reinstall (mutex with --token-only) |
| `cc-clip setup <host> --auto-recover` | Same recovery flow via setup path |
| `cc-clip connect <host> --force` | Full redeploy ignoring cache |
| `cc-clip connect <host> --adopt-foreign-shim` | If a regular file cc-clip did not write occupies `xclip` / `wl-paste` / `wl-copy` in `~/.local/bin`, move it to `<path>.cc-clip-real` and fall back to it instead of refusing. Connect-only: `setup` rejects it; run `setup` first, then `connect` with the flag |
| `cc-clip connect <host> --no-notify` | Deploy without notification setup (nonce sync and agent hooks) |
| `cc-clip connect <host> --no-hooks` / `--hooks` | Persistently disable / re-enable Claude Code hook injection |
| `cc-clip connect <host> --local-bin <path>` | Deploy this pre-downloaded remote binary instead of fetching one |
| `cc-clip connect <host> --port <n>` | Use a tunnel port other than 18339 |
| `cc-clip tunnel run <host>` | **Experimental, on your local machine (not the remote).** Foreground managed tunnel: a private SSH master holds the reverse forward, probes the daemon through it and reconnects with backoff. Needs `connect <host> --force` once (for the remote identity helper at `~/.local/bin/cc-clip`); not supported for `--use-remote-bin` hosts. `--port` for a non-default port, `--reset` to clear persisted crash-loop state. Any other `ssh <host>` that takes the `RemoteForward` from your SSH config holds the port first (`port-conflict`); comment that line out while trying it |
| `cc-clip hosts list` | Show hosts this machine has deployed to (version, Codex, last seen) |
| `cc-clip hosts forget <host>` | Stop tracking a host locally; the remote is not touched |
| `cc-clip uninstall` | **On the remote host:** remove the clipboard shim and restore a program adopted with `--adopt-foreign-shim`. `--target wl-paste` on Wayland (also covers `wl-copy`); `--path` for a non-default install directory |
| `cc-clip uninstall --host <host>` | **On your local machine:** remove the managed Claude hooks/wrapper and the PATH marker from the remote. Run the remote `cc-clip uninstall` first. Also tries a local shim, which warns harmlessly on a Mac |
| `cc-clip uninstall --codex` | Remove Codex support (local) |
| `cc-clip uninstall --codex --host <host>` | Remove Codex support from remote |

## Windows workflow

| Command | Description |
|---------|-------------|
| `cc-clip send [<host>] [<file>]` | Upload clipboard image, or a saved image file, to a remote file |
| `cc-clip send [<host>] [<file>] --paste` | Windows: paste the uploaded remote path into the active window |
| `cc-clip hotkey [<host>]` | Windows: run a background remote-paste hotkey listener |
| `cc-clip hotkey [<host>] --no-restore` | Windows: leave the remote path on the clipboard instead of restoring the image (fallback for terminals that drop synthetic keystrokes) |
| `cc-clip hotkey --enable-autostart` | Windows: start the hotkey listener automatically at login |
| `cc-clip hotkey --disable-autostart` | Windows: remove hotkey auto-start at login |
| `cc-clip hotkey --status` | Windows: show hotkey status |
| `cc-clip hotkey --stop` | Windows: stop the hotkey listener |

## On the remote host

| Command | Description |
|---------|-------------|
| `some-command \| cc-clip copy` | Put the piped text on your local clipboard verbatim ([reverse copy](reverse-copy.md)) |
| `cc-clip paste` | Save the local clipboard image to a remote file and print its path (`--out-dir` to choose where, `--port` for a non-default tunnel port) |

## Notifications

| Command | Description |
|---------|-------------|
| `cc-clip notify --title T --body B` | Send a generic notification through the tunnel; titled `[unverified] T` unless `--trusted`; `--port` (or `CC_CLIP_PORT`) for a non-default port |
| `cc-clip notify --from-codex "$1"` | Parse Codex JSON arg and notify |
| `cc-clip notify --from-codex-stdin` | Read Codex JSON from stdin and notify |

Copying *from* the remote back to your local clipboard — including neovim yanks
and tmux copy-mode — is covered in [reverse copy](reverse-copy.md).

## Diagnostics

| Command | Description |
|---------|-------------|
| `cc-clip doctor` | Local health check |
| `cc-clip doctor --host <host>` | End-to-end health check, including when each agent last delivered a notification (`delivery-receipt` lines, never a failure) |
| `cc-clip status` | Show component status |
| `cc-clip version` | Show version |

## Internal (run by cc-clip itself)

| Command | Description |
|---------|-------------|
| `cc-clip install` | Install the shim; `connect` runs it on the remote (`--target`, `--path`, `--port`, `--adopt-foreign-shim`) |
| `cc-clip plugin run <name>` | Notification hook each agent calls: `claude-notify`, `codex-notify`, `opencode-notify`, `agy-notify`, `cursor-notify` |
| `cc-clip tunnel probe-identity` | Remote side of the managed tunnel's identity check; `tunnel run` invokes it over SSH |
| `cc-clip x11-bridge` | Serve the clipboard to Codex through Xvfb; started by `connect --codex` |

## Environment variables

| Setting | Default | Env Var |
|---------|---------|---------|
| Port | 18339 | `CC_CLIP_PORT` |
| Token TTL | 30d | `CC_CLIP_TOKEN_TTL` |
| Output dir | `$XDG_RUNTIME_DIR/claude-images` | `CC_CLIP_OUT_DIR` |
| Max clipboard text | 1MB | `CC_CLIP_MAX_TEXT_MB` |
| Max clipboard image | 20MB | `CC_CLIP_MAX_IMAGE_MB` |
| Probe timeout | 500ms | `CC_CLIP_PROBE_TIMEOUT_MS` |
| Fetch timeout | 5000ms | `CC_CLIP_FETCH_TIMEOUT_MS` |
| Debug logs | off | `CC_CLIP_DEBUG=1` |

> Size limits apply to the local daemon (`cc-clip serve`) and Go fetch clients
> (`cc-clip paste`, x11-bridge) in the process where the env var is set.

> `cc-clip --help` always shows the authoritative flag list for the installed version.
