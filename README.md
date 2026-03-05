# cc-clip

Paste images into remote Claude Code over SSH — as if it were local.

## The Problem

When running Claude Code on a remote server via SSH, `Ctrl+V` image paste doesn't work.
The remote `xclip` reads the server's clipboard, not your local Mac's clipboard.

## The Solution

cc-clip creates a transparent bridge between your local clipboard and the remote server:

```
Local Mac clipboard  -->  SSH tunnel  -->  xclip shim  -->  Claude Code
```

No changes to Claude Code. No terminal-specific hacks. Just works.

## Quick Start

**1. Install locally (Mac):**

```bash
curl -fsSL https://raw.githubusercontent.com/shunmei/cc-clip/main/scripts/install.sh | sh
```

**2. Start the daemon and connect:**

```bash
# Terminal 1: Start local clipboard daemon
cc-clip serve

# Terminal 2: Deploy to your remote server
cc-clip connect myserver
```

**3. Add SSH port forwarding** (if not already configured):

```
# ~/.ssh/config
Host myserver
    RemoteForward 18339 127.0.0.1:18339
```

**4. Done.** `Ctrl+V` in remote Claude Code now pastes images from your Mac.

## How It Works

1. **Local daemon** (`cc-clip serve`) — Reads your Mac clipboard, serves images via HTTP on `127.0.0.1:18339`
2. **SSH tunnel** (`RemoteForward`) — Forwards the daemon port to the remote server
3. **xclip shim** — Intercepts only the clipboard calls Claude Code makes, fetches image data through the tunnel, passes everything else to the real `xclip`

### Security

- Daemon listens on loopback only (`127.0.0.1`)
- Session-scoped token with TTL (default 12h)
- Token transmitted via stdin, not command-line arguments
- All non-shim calls pass through to real `xclip` unchanged

## Commands

| Command | Description |
|---------|-------------|
| `cc-clip serve` | Start local clipboard daemon |
| `cc-clip connect <host>` | Deploy to remote server (one command) |
| `cc-clip install` | Install xclip shim on remote |
| `cc-clip uninstall` | Remove xclip shim |
| `cc-clip paste` | Manually fetch clipboard image (fallback) |
| `cc-clip doctor` | Local health check |
| `cc-clip doctor --host H` | End-to-end health check |
| `cc-clip status` | Show component status |

## Configuration

All settings have sensible defaults. Override via flags or environment variables:

| Setting | Default | Env Var |
|---------|---------|---------|
| Port | 18339 | `CC_CLIP_PORT` |
| Token TTL | 12h | `CC_CLIP_TOKEN_TTL` |
| Output dir | `$XDG_RUNTIME_DIR/claude-images` | `CC_CLIP_OUT_DIR` |
| Probe timeout | 500ms | `CC_CLIP_PROBE_TIMEOUT_MS` |
| Fetch timeout | 5000ms | `CC_CLIP_FETCH_TIMEOUT_MS` |
| Debug logs | off | `CC_CLIP_DEBUG=1` |

## Requirements

**Local (Mac):**
- macOS 13+
- `pngpaste` (`brew install pngpaste`)
- `curl`

**Remote (Linux):**
- `curl` (for shim HTTP calls)
- `bash` (shim is a bash script)
- SSH access with `RemoteForward` capability

## Platform Support

| Local | Remote | Status |
|-------|--------|--------|
| macOS (arm64/amd64) | Linux (amd64/arm64) | GA |

## Troubleshooting

```bash
# Check everything at once
cc-clip doctor --host myserver

# Enable debug logging on the shim
ssh myserver 'CC_CLIP_DEBUG=1 xclip -selection clipboard -t TARGETS -o'
```

## Related Issues

- [anthropics/claude-code#5277](https://github.com/anthropics/claude-code/issues/5277) — Image paste in SSH sessions
- [anthropics/claude-code#29204](https://github.com/anthropics/claude-code/issues/29204) — xclip/wl-paste dependency
- [ghostty-org/ghostty#10517](https://github.com/ghostty-org/ghostty/discussions/10517) — SSH image paste discussion

## License

MIT
