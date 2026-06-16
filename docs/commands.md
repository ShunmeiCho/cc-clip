# Commands Reference

Complete cc-clip command reference. For the 10 most common commands, see the [Commands section of the README](../README.md#commands).

## Local daemon

| Command | Description |
|---------|-------------|
| `cc-clip serve` | Start daemon in foreground |
| `cc-clip serve --rotate-token` | Start daemon with forced new token |
| `cc-clip service install` | Install local daemon service (macOS launchd / Windows logon launcher) |
| `cc-clip service uninstall` | Remove local daemon service |
| `cc-clip service status` | Show service status |

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
| `cc-clip connect <host> --all` | Every target (Claude + Codex + opencode + agy) |
| `cc-clip connect <host> --token-only` | Sync token only (fast) |
| `cc-clip connect <host> --auto-recover` | Recover from v0.7.0 wrapper corruption + reinstall (mutex with --token-only) |
| `cc-clip setup <host> --auto-recover` | Same recovery flow via setup path |
| `cc-clip connect <host> --force` | Full redeploy ignoring cache |
| `cc-clip uninstall` | Remove local xclip shim only |
| `cc-clip uninstall --host <host>` | Remove from remote: Claude managed hooks/wrapper, PATH marker (local shim best-effort) |
| `cc-clip uninstall --codex` | Remove Codex support (local) |
| `cc-clip uninstall --codex --host <host>` | Remove Codex support from remote |

## Windows workflow

| Command | Description |
|---------|-------------|
| `cc-clip send [<host>] [<file>]` | Upload clipboard image, or a saved image file, to a remote file |
| `cc-clip send [<host>] [<file>] --paste` | Windows: paste the uploaded remote path into the active window |
| `cc-clip hotkey [<host>]` | Windows: run a background remote-paste hotkey listener |
| `cc-clip hotkey --enable-autostart` | Windows: start the hotkey listener automatically at login |
| `cc-clip hotkey --disable-autostart` | Windows: remove hotkey auto-start at login |
| `cc-clip hotkey --status` | Windows: show hotkey status |
| `cc-clip hotkey --stop` | Windows: stop the hotkey listener |

## Notifications

| Command | Description |
|---------|-------------|
| `cc-clip notify --title T --body B` | Send a generic notification through the tunnel |
| `cc-clip notify --from-codex "$1"` | Parse Codex JSON arg and notify |
| `cc-clip notify --from-codex-stdin` | Read Codex JSON from stdin and notify |

## Diagnostics

| Command | Description |
|---------|-------------|
| `cc-clip doctor` | Local health check |
| `cc-clip doctor --host <host>` | End-to-end health check |
| `cc-clip status` | Show component status |
| `cc-clip version` | Show version |

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
