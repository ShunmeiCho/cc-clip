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
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh
```

**2. Start the daemon:**

```bash
# Option A: Auto-start via launchd (recommended, macOS)
cc-clip service install

# Option B: Manual foreground
cc-clip serve
```

**3. Add SSH port forwarding** (if not already configured):

```
# ~/.ssh/config
Host myserver
    RemoteForward 18339 127.0.0.1:18339
```

> **Important:** If you use `ControlMaster` for SSH connection multiplexing, see [Troubleshooting — SSH ControlMaster](#ssh-controlmaster-breaks-remoteforward) below.

**4. Deploy to remote:**

```bash
cc-clip connect myserver
```

This handles everything in one command: detect arch, upload binary, install shim, sync token, fix PATH, verify tunnel. Subsequent runs are incremental — only changed components are re-deployed.

**5. Done.** `Ctrl+V` in remote Claude Code now pastes images from your Mac.

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
| `cc-clip serve` | Start local clipboard daemon (foreground) |
| `cc-clip serve --rotate-token` | Start daemon with forced new token |
| `cc-clip service install` | Install macOS launchd service (auto-start, background) |
| `cc-clip service uninstall` | Remove launchd service |
| `cc-clip service status` | Show launchd service status |
| `cc-clip connect <host>` | Deploy to remote server (incremental, one command) |
| `cc-clip connect <host> --token-only` | Only sync token (fast, no binary upload) |
| `cc-clip connect <host> --force` | Full redeploy ignoring cached state |
| `cc-clip install` | Install xclip shim on remote |
| `cc-clip uninstall` | Remove xclip shim |
| `cc-clip uninstall --host H` | Remove shim and clean up remote PATH marker |
| `cc-clip paste` | Manually fetch clipboard image (fallback) |
| `cc-clip doctor` | Local health check |
| `cc-clip doctor --host H` | End-to-end health check |
| `cc-clip status` | Show component status (daemon, token, launchd) |

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
- `xclip` (`sudo apt install xclip`) — the shim wraps it; the real binary must exist as fallback
- `curl` (for shim HTTP calls)
- `bash` (shim is a bash script)
- `~/.local/bin` in `PATH` (must have higher priority than `/usr/bin`)
- SSH access with `RemoteForward` capability

## Platform Support

| Local | Remote | Status |
|-------|--------|--------|
| macOS (arm64/amd64) | Linux (amd64/arm64) | GA |

## Troubleshooting

### Quick Diagnostics

```bash
# Check everything at once
cc-clip doctor --host myserver

# Enable debug logging on the shim
ssh myserver 'CC_CLIP_DEBUG=1 xclip -selection clipboard -t TARGETS -o'
```

### Step-by-Step Verification

If image paste isn't working, run these checks **in order** to isolate the problem:

```bash
# 1. Local: Is the daemon running?
curl -s http://127.0.0.1:18339/health
# Expected: {"status":"ok"}

# 2. Remote: Is the tunnel forwarding?
ssh myserver "curl -s http://127.0.0.1:18339/health"
# Expected: {"status":"ok"}

# 3. Remote: Is the shim taking priority over real xclip?
ssh myserver "which xclip"
# Expected: /home/<user>/.local/bin/xclip  (NOT /usr/bin/xclip)

# 4. Remote: Does the shim intercept correctly? (copy an image on Mac first)
ssh myserver 'CC_CLIP_DEBUG=1 xclip -selection clipboard -t TARGETS -o'
# Expected: image/png
```

### SSH ControlMaster Breaks RemoteForward

**Symptom:** `cc-clip connect` reports "tunnel verified", but the tunnel doesn't work in your interactive SSH session. `curl -s http://127.0.0.1:18339/health` hangs on the remote.

**Cause:** If you use SSH `ControlMaster auto` (connection multiplexing), the first SSH connection becomes the "master". All subsequent connections **reuse the master** — even if you later add `RemoteForward` to your config. The old master connection does not have the port forwarding, so the tunnel silently fails.

`ssh -O exit` to kill the master often doesn't help because the control socket hash (`%C`) may differ between sessions.

**Fix:** Disable `ControlMaster` for hosts that use `RemoteForward`:

```
# ~/.ssh/config
Host myserver
    HostName 10.x.x.x
    User myuser
    RemoteForward 18339 127.0.0.1:18339
    ControlMaster no
    ControlPath none
```

This ensures every SSH connection creates a fresh tunnel. The trade-off is slightly slower connection setup (no multiplexing), but it guarantees `RemoteForward` works reliably.

**Alternatively**, if you want to keep `ControlMaster` for other hosts, ensure it's only disabled for this specific host, while keeping the global `Host *` setting for others.

### Stale sshd Process Blocks RemoteForward

**Symptom:** `ssh myserver` shows `Warning: remote port forwarding failed for listen port 18339`. The tunnel never works regardless of how many times you reconnect.

**Cause:** A previous SSH session (from an older `cc-clip connect` or a crashed SSH connection) left a stale `sshd` child process on the remote server that is still holding port 18339. New SSH connections cannot bind `RemoteForward` to a port that's already in use.

This was caused by `cc-clip connect` (before v0.1.1) inheriting `RemoteForward` from `~/.ssh/config`, competing with the user's interactive SSH session for the same port. Fixed in [c306bda](https://github.com/ShunmeiCho/cc-clip/commit/c306bda) with `ClearAllForwardings=yes`.

**Diagnosis (on remote):**

```bash
sudo ss -tlnp | grep 18339
# Shows: sshd,pid=XXXXX listening on 18339
```

**Fix:**

```bash
# On remote: kill the stale sshd process
sudo kill <PID>

# Then reconnect from local (forwarding should succeed now)
ssh myserver
curl -s http://127.0.0.1:18339/health
# Expected: {"status":"ok"}
```

**Prevention:** Update to the latest cc-clip. The `connect` command now uses `ClearAllForwardings=yes` for its internal SSH session, so it never competes for the RemoteForward port.

### Daemon Restart Invalidates Token

**Symptom:** "fetch type failed" or "token invalid" in shim debug logs.

**Cause:** Each time `cc-clip serve` starts, it generates a **new token**. The remote server still has the old token from the previous session.

**Fix (recommended):** Use `cc-clip service install` to run the daemon via launchd. The daemon auto-restarts and tokens are persisted with a 12h TTL — restarts reuse the existing token if it hasn't expired.

**Fix (manual):** If the token did change, sync it without re-uploading the binary:

```bash
cc-clip connect <host> --token-only
```

To force a new token: `cc-clip serve --rotate-token`.

### Remote `xclip` Not Installed

**Symptom:** Shim debug log shows `/usr/bin/xclip: No such file or directory`.

**Cause:** The shim needs the real `xclip` binary as a fallback for non-image clipboard operations (e.g., text paste).

**Fix:**

```bash
sudo apt install xclip    # Debian/Ubuntu
sudo yum install xclip    # RHEL/CentOS
```

Then re-run `cc-clip connect <host>` to re-detect the real binary path.

### `~/.local/bin` Not in PATH

**Symptom:** `cc-clip connect` shows WARNING: `'which xclip' resolves to /usr/bin/xclip, not ~/.local/bin/xclip`.

**Cause:** The shim is installed to `~/.local/bin/` but it's not first in PATH, so the system uses `/usr/bin/xclip` instead.

**Fix:** `cc-clip connect` now auto-detects your remote shell (bash/zsh) and injects a PATH marker into the appropriate rc file. If auto-fix didn't work, add manually to your remote `~/.bashrc` (or `~/.zshrc`):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Verify with `which xclip` — it should point to `~/.local/bin/xclip`.

### Empty Image Data (API Error 400)

**Symptom:** Claude Code returns `API Error: 400 — image cannot be empty`. The conversation becomes corrupted and all subsequent image pastes fail in the same session.

**Cause:** A race condition where the clipboard content changes between the TARGETS check and the image fetch. The daemon returns HTTP 204 (No Content), but `curl -sf` treats 2xx as success and writes 0 bytes. The shim outputs empty data, and Claude Code sends an empty base64 image to the API.

**Fix:** This was fixed in [cc6b0b2](https://github.com/ShunmeiCho/cc-clip/commit/cc6b0b2). The shim now checks that downloaded data is non-empty before returning success. If you hit this error:

1. In Claude Code, run `/clear` or start a new session (the old conversation is corrupted)
2. Update to the latest cc-clip and re-run `cc-clip connect <host>`

### Launchd Daemon Returns "empty" for Image Clipboard

**Symptom:** `cc-clip service install` is running, but `/clipboard/type` returns `{"type":"empty"}` even when you have an image in your Mac clipboard. Running `cc-clip serve` in the foreground works correctly.

**Cause:** macOS `launchd` does not source your shell profile, so `PATH` doesn't include Homebrew directories (`/opt/homebrew/bin` on Apple Silicon, `/usr/local/bin` on Intel). The daemon can't find `pngpaste`, silently falls through to `pbpaste`, which either hangs or returns empty for image clipboard content.

**Fix:** This was fixed in [6b4b5e2](https://github.com/ShunmeiCho/cc-clip/commit/6b4b5e2). The fix has two layers:
1. The launchd plist now injects `PATH` including Homebrew directories
2. The clipboard reader falls back to well-known paths (`/opt/homebrew/bin/pngpaste`, `/usr/local/bin/pngpaste`) when `LookPath` fails

If you're on an older version, reinstall the service to regenerate the plist:

```bash
cc-clip service uninstall
cc-clip service install
```

### No Image in Clipboard

**Symptom:** Shim returns `image/png` for TARGETS but Claude Code says "No image found in clipboard".

**Cause:** You may not have an image in your Mac clipboard. The shim falls back to real `xclip` which reads the remote server's (empty) clipboard.

**Fix:** Copy an image on your Mac first:
- **Screenshot to clipboard:** `Cmd + Shift + Ctrl + 4` (select area) or `Cmd + Shift + Ctrl + 3` (full screen)
- **Copy from an app:** Right-click an image → Copy Image

## Known Limitations

The following are known pain points that we plan to improve iteratively:

| Issue | Description | Status |
|-------|-------------|--------|
| Token re-sync on daemon restart | Every `cc-clip serve` restart generates a new token, requiring a full `cc-clip connect` to re-sync | ✅ Fixed — token persistence with TTL, `--token-only` flag |
| Full redeploy on every `connect` | `connect` re-compiles and re-uploads the binary even when only the token changed | ✅ Fixed — incremental deploy with hash-based skip |
| SSH passphrase prompted multiple times | `connect` makes multiple SSH calls internally, each prompting for passphrase | ✅ Fixed — SSH ControlMaster session reuse |
| Remote PATH not auto-configured | `connect` detects `~/.local/bin` not in PATH but doesn't fix it automatically | ✅ Fixed — auto-detect shell and inject PATH marker |
| No daemon auto-start | Daemon runs in foreground; `nohup` is a workaround, no launchd/systemd integration yet | ✅ Fixed — `cc-clip service install` for macOS launchd |

Contributions and ideas welcome — see [Issues](https://github.com/ShunmeiCho/cc-clip/issues).

## Related Issues

- [anthropics/claude-code#5277](https://github.com/anthropics/claude-code/issues/5277) — Image paste in SSH sessions
- [anthropics/claude-code#29204](https://github.com/anthropics/claude-code/issues/29204) — xclip/wl-paste dependency
- [ghostty-org/ghostty#10517](https://github.com/ghostty-org/ghostty/discussions/10517) — SSH image paste discussion

## License

MIT
