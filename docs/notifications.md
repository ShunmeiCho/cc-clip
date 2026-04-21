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
| Claude Code | ⚠️ Manual — add `cc-clip-hook` to `Stop` / `Notification` hooks in `~/.claude/settings.json` | See setup below |
| Codex CLI | ✅ If `~/.codex/` exists on the remote | Wired during `cc-clip connect` |
| opencode | ❌ Not yet supported out of the box | You can wire your own notifier using the `/notify` endpoint |

## Setup (Claude Code)

### Step 1: Make sure the local daemon is running

```bash
cc-clip serve               # foreground
# OR
cc-clip service install     # launchd auto-start
```

### Step 2: Configure remote Claude Code hooks

The easiest way is to **ask Claude Code itself to do it**. SSH into your server, start Claude Code, and paste this prompt:

```
Please add cc-clip-hook to my Claude Code hooks configuration. Add it to both Stop and Notification hooks in ~/.claude/settings.json. The command is just "cc-clip-hook" (it's already in PATH at ~/.local/bin/). Keep any existing hooks (like ralph-wiggum) — just append cc-clip-hook alongside them. Show me the diff before and after.
```

Claude Code will read your current `settings.json`, add the hooks correctly, and show you the changes.

### Step 2 (manual alternative)

Edit `~/.claude/settings.json` on the **remote server** and add `cc-clip-hook` to the `Stop` and `Notification` hook arrays:

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

If you already have hooks (e.g., `ralph-wiggum-stop.sh`), add a new entry to the array — don't replace existing ones.

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
