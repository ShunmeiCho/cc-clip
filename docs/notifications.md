# SSH Notifications

When Claude Code or Codex CLI runs on a remote server, **notifications don't work over SSH** — `TERM_PROGRAM` isn't forwarded, hooks execute on the remote where `terminal-notifier` doesn't exist, and tmux swallows OSC sequences.

cc-clip solves this by acting as a **notification transport bridge**: remote hook events travel through the same SSH tunnel used for clipboard, and the local daemon delivers them to your macOS notification system (or cmux if installed).

## What you'll see

| Event | Notification | Example |
|-------|-------------|---------|
| Claude finishes responding | "Claude stopped" + last message preview | `Claude stopped: I've implemented the notification bridge...` |
| Claude needs tool approval | "Tool approval needed" + tool name | `Tool approval needed: Claude wants to Edit cmd/main.go` |
| Codex task completes | "Codex" + completion message | `Codex: Added error handling to fetch module` |
| Image pasted via Ctrl+V | "cc-clip #N" + fingerprint + dimensions | `cc-clip #3: a1b2c3d4 . 1920x1080 . PNG` |
| Duplicate image detected | Same as above + duplicate marker | `cc-clip #4: Duplicate of #2` |

Image paste notifications help you track what was pasted without leaving your workflow:

- **Sequence number** (#1, #2, #3...) lets you detect gaps (e.g., #1 → #3 means #2 was lost).
- **Duplicate detection** alerts when the same image is pasted twice within 5 images.
- **Click notification** to open the full image in Preview.app (macOS, requires `terminal-notifier`).

## Coverage by CLI

| CLI | Auto-configured by `cc-clip connect`? | How |
|-----|----------------------------------------|-----|
| Claude Code | ✅ Managed hooks in `~/.claude/settings.json` | Wired during `cc-clip connect` |
| Codex CLI | ✅ If a Codex target (`--codex`/`--all`) is selected and `~/.codex/` exists | Wired during `cc-clip connect` |
| opencode | ✅ Plugin dropped into `~/.config/opencode/plugins/` if `opencode` is installed | Wired during `cc-clip connect` when an opencode target (`--opencode`/`--all`) is selected |
| Antigravity (agy) | ✅ `agy-notify` plugin installed via the `agy` CLI if `agy` is installed | Wired during `cc-clip connect` when an Antigravity target (`--agy`/`--all`) is selected |

## Setup (Claude Code)

### Step 1: Make sure the local daemon is running

```bash
cc-clip serve               # foreground
# OR
cc-clip service install     # launchd auto-start
```

### Step 2: Configure remote Claude Code hooks

Run `cc-clip connect <host>`. It installs `cc-clip-hook` and merges managed
Stop/Notification hooks into the remote `~/.claude/settings.json` without
overwriting your existing hooks.

Use `--no-hooks` to persistently opt out, and `--hooks` to re-enable managed
hook installation:

```bash
cc-clip connect myserver --no-hooks
cc-clip connect myserver --hooks
```

If automatic settings merge fails, cc-clip prints a fallback config you can add
manually. The hook shape is:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "cc-clip-hook" }
        ]
      }
    ],
    "Notification": [
      {
        "hooks": [
          { "type": "command", "command": "cc-clip-hook" }
        ]
      }
    ]
  }
}
```

If you already have hooks (e.g., `ralph-wiggum-stop.sh`), keep them and add a
new entry to the array.

**Restart Claude Code** after editing (hooks are read at startup).

### Step 3 (Codex only): no manual work

Codex notification is auto-configured by `cc-clip connect` if `~/.codex/` exists on the remote.

### Step 4: Register the notification nonce (if you haven't used `cc-clip connect`)

```bash
# On local Mac — generate nonce and write to remote
NONCE=$(openssl rand -hex 32)
curl -s -X POST -H "Authorization: Bearer $(head -1 ~/.cache/cc-clip/session.token)" \
  -H "User-Agent: cc-clip/0.1" -H "Content-Type: application/json" \
  -d "{\"nonce\":\"$NONCE\"}" http://127.0.0.1:18339/register-nonce
ssh myserver "mkdir -p ~/.cache/cc-clip && echo '$NONCE' > ~/.cache/cc-clip/notify.nonce && chmod 600 ~/.cache/cc-clip/notify.nonce"
```

> **Note:** `cc-clip connect` handles steps 2-4 automatically. Manual setup is only needed if you use plain `ssh` instead of `cc-clip connect`.

## Customizing sound & icon (macOS)

Notification **sound** and the **app icon** are configured with local
environment variables read by the daemon (`cc-clip serve`). They are read on the
local Mac only — never from the remote payload — so a remote agent cannot change
your local sound or icon.

> **Note:** These settings apply to the **native macOS notification path**
> (`terminal-notifier` / osascript). If `cmux` is installed, the delivery chain
> tries it first and — when it succeeds — sound and icon are skipped, because the
> `cmux` (tmux) path only carries a title and body.

Set them in the environment the daemon runs in:

- **Foreground** (`cc-clip serve` in a shell): `export` the variable before starting.
- **launchd service**: `launchctl setenv NAME value`, then restart the service
  (`cc-clip service uninstall && cc-clip service install`). Note that
  `launchctl setenv` is **session/bootstrap-scoped** — it is inherited by the
  service the next time it starts, but it is *not* written into the cc-clip
  `plist`, so reapply it after a reboot or re-login (or `export` the vars in the
  shell that launches `cc-clip serve`).

### Sound

By default only **tool-approval** prompts play a sound (`Glass`); completion and
idle notifications are silent. Override per urgency tier:

| Variable | Applies to | Default |
|----------|-----------|---------|
| `CC_CLIP_SOUND_CRITICAL` | Tool approval needed (urgency 2) | `Glass` |
| `CC_CLIP_SOUND_ATTENTION` | Idle / interrupted / Codex / opencode / Antigravity (urgency 1) | silent |
| `CC_CLIP_SOUND_CALM` | "Claude finished" at end of turn (urgency 0) | silent |

Values are macOS system sound names: `Basso`, `Blow`, `Bottle`, `Frog`, `Funk`,
`Glass`, `Hero`, `Morse`, `Ping`, `Pop`, `Purr`, `Sosumi`, `Submarine`, `Tink`.
Use `none` / `off` / `silent` to mute a tier; unrecognized names are treated as
silent. A per-notification `sound` field in a `cc-clip notify` / `/notify`
payload still overrides these tier defaults.

```bash
# Gentle end-of-turn chime, distinct approval sound
launchctl setenv CC_CLIP_SOUND_CALM Tink
launchctl setenv CC_CLIP_SOUND_CRITICAL Sosumi
```

### App icon

`CC_CLIP_NOTIFY_APP_ICON` sets a custom notification icon, pointing at a local
`.png`/`.icns` file:

```bash
launchctl setenv CC_CLIP_NOTIFY_APP_ICON "$HOME/.config/cc-clip/icon.png"
```

Limitations:

- Honored **only on the `terminal-notifier` path** (`brew install terminal-notifier`).
  The osascript fallback cannot set a custom app icon — macOS derives it from the
  calling process — so without `terminal-notifier` the icon is unchanged.
- A missing file or a directory is ignored silently (no icon, no error).

## Troubleshooting

### Notifications don't appear

Step-by-step verification (on the remote server):

```bash
# 1. Is the tunnel working?
curl -sf --connect-timeout 2 http://127.0.0.1:18339/health
# Expected: {"status":"ok"}

# 2. Is the hook script the correct version?
grep "curl" ~/.local/bin/cc-clip-hook
# Expected: a curl command with --connect-timeout

# 3. Is the nonce file present?
cat ~/.cache/cc-clip/notify.nonce
# Expected: a 64-character hex string

# 4. Manual test:
echo '{"hook_event_name":"Stop","stop_hook_reason":"stop_at_end_of_turn","last_assistant_message":"test"}' | cc-clip-hook
# Expected: local Mac shows notification popup

# 5. Check health log for failures:
cat ~/.cache/cc-clip/notify-health.log
# If exists: shows timestamps and HTTP error codes
```

| Problem | Fix |
|---------|-----|
| Tunnel down (step 1 fails) | Kill stale sshd: `sudo kill $(sudo lsof -ti :18339)`, then reconnect SSH |
| Old hook script (step 2 empty) | Reinstall: `cc-clip connect myserver` or manually copy the script |
| Missing nonce (step 3 fails) | Register nonce (see Step 4 above) |
| Daemon running old binary | Rebuild (`make build`) and restart (`cc-clip serve`) |

## Security

- Notification transport uses a **separate per-connect nonce**, not the clipboard session token. The two can be rotated independently.
- The nonce is stored in `~/.cache/cc-clip/notify.nonce` with `0600` permissions.
- Hook-delivered notifications are marked `verified`; raw `cc-clip notify` JSON requests are marked `[unverified]` in the title.
