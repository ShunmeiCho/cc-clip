# Notification Bridge Design

**Date:** 2026-04-01
**Status:** Design Complete, Pending Implementation
**Version:** v0.6.0 (planned)
**Prerequisite:** v0.5.0 (clipboard preview notification)

## Problem

When running Claude Code or Codex CLI on a remote server via SSH, all meaningful notification mechanisms are broken:

| Scenario | What works | What breaks |
|----------|-----------|-------------|
| Local (no SSH) | Everything | - |
| Direct SSH (no tmux) | BEL may pass through (no content) | Hook commands (execute on remote, no local notification tools); `TERM_PROGRAM` not forwarded so auto-detection fails; OSC 9/777 unreliable |
| SSH + tmux | Nothing | All of the above + tmux swallows OSC sequences |

**Root cause:** All existing notification solutions assume the AI tool runs on the same machine as the notification system:
- **cmux:** Injects Claude Code hooks via wrapper script; hooks communicate via local Unix socket. Neither exists on the remote.
- **Ghostty/iTerm2/Kitty:** Detect OSC 9/99/777 escape sequences in terminal output. SSH doesn't forward `TERM_PROGRAM`, and tmux swallows escape sequences.
- **Claude Code hooks:** Execute shell commands on the remote machine. `terminal-notifier`, `osascript`, `cmux notify` don't exist there.
- **Codex CLI notify:** Same problem as hooks; the configured command runs on the remote.

**Opportunity:** cc-clip already maintains a reverse HTTP tunnel (SSH RemoteForward) from remote to local. Adding a `/notify` endpoint to the daemon creates an application-level notification bridge that naturally traverses SSH.

## Positioning

**cc-clip = Notification Transport Bridge, NOT Notification UI**

cc-clip does not render notifications itself. It transports notification data from the remote through the SSH tunnel and delivers to the local system's native notification mechanisms.

```
cc-clip's role:
  Remote hook/notify → [SSH tunnel] → Local daemon → Delivery adapter → Native notification system
                                                       ↑
                                            cmux / Ghostty / macOS / ...
```

Advantage: Users get the notification quality of their chosen terminal/tool, not a separate second-class experience.

## Architecture

```
+---------------- Remote Server ------------------+
|                                                  |
|  Claude Code              Codex CLI              |
|      |                        |                  |
|  Hook events             notify config           |
|  (Stop/Notification)     (task complete)          |
|      |                       |                   |
|      |                       |                   |
|      v                       v                   |
|  cc-clip-hook.sh         cc-clip notify          |
|  (stdin JSON -> curl)    (CLI -> curl)            |
|      |                       |                   |
|      +----------+------------+                   |
|                 |                                 |
|     HTTP POST /notify (127.0.0.1:18339)          |
|         (via SSH RemoteForward tunnel)            |
|                                                  |
+---------------------|----------------------------+
                      | SSH tunnel
+---------------------v----------------------------+
|               Local Mac                           |
|                                                   |
|  cc-clip daemon (127.0.0.1:18339)                |
|      |                                            |
|  POST /notify handler                             |
|      +-- Parse payload (hook JSON or generic)     |
|      +-- Classify notification type               |
|      +-- Dedup / throttle (10-15s window)         |
|      +-- Create NotifyEnvelope                    |
|      +-- Route: critical → criticalCh (blocking)  |
|      +--         normal → notifyCh (drop on full) |
|              |                                    |
|  RunNotifier goroutine                            |
|      +-- DeliveryChain (try cmux → macOS)         |
|      +-- Deliver notification                     |
|                                                   |
|  Result: native notification in user's tool       |
+---------------------------------------------------+
```

## V1 Scope

### In scope
- `POST /notify` endpoint on daemon
- Dual payload format (Claude hook JSON passthrough + generic schema)
- Notification classifier (extract type/title/body from hook JSON)
- Delivery adapters: macOS Notification Center (fallback) + cmux
- `cc-clip-hook.sh` script for remote Claude Code hook integration
- `cc-clip notify` subcommand for generic CLI use
- Auto-configuration of Codex `~/.codex/config.toml` during `connect` (not `--codex`-specific; see Design Note 1)
- Dedup/throttle per source+type+title+body hash in 10-15s window
- Priority channel for critical notifications (permission_prompt) — never dropped
- Hook transport health: failure counter + log + health probe before default-on
- Notification nonce: per-connect secret for hook auth, separate from clipboard token
- Multi-host: host alias in notification title (e.g., `[venus] Claude Code`)
- Default enabled, `--no-notify` to opt-out

### Out of scope (V2+)
- OSC sequence injection to terminal PTY (PTY discovery is complex)
- Action buttons (Approve/Deny) with bidirectional communication
- Web dashboard
- Windows/Linux local delivery adapters
- Auto-injection of Claude Code hook settings (V1 generates paste-ready config)

## Notification Envelope Model

The existing `NotifyEvent` is image-transfer specific. Rather than expanding it, introduce a unified envelope with kind-specific payloads:

```go
// internal/daemon/envelope.go

type NotifyKind string

const (
    KindImageTransfer NotifyKind = "image_transfer"
    KindToolAttention NotifyKind = "tool_attention"
    KindGenericMessage NotifyKind = "generic_message"
)

type NotifyEnvelope struct {
    Kind      NotifyKind
    Source    string     // "clipboard" | "claude_hook" | "codex_notify" | "cli"
    Host      string     // remote host alias, e.g. "venus"
    Timestamp time.Time

    // Kind-specific payload (exactly one is non-nil)
    ImageTransfer  *ImageTransferPayload
    ToolAttention  *ToolAttentionPayload
    GenericMessage *GenericMessagePayload
}

type ImageTransferPayload struct {
    SessionID   string
    Seq         int
    Fingerprint string
    ImageData   []byte
    Format      string
    Width       int
    Height      int
    DuplicateOf int
}

type ToolAttentionPayload struct {
    SessionID  string
    HookType   string   // "stop" | "notification"
    StopReason string   // "stop_at_end_of_turn", etc.
    NotifType  string   // "permission_prompt" | "idle_prompt" | etc.
    ToolName   string
    ToolInput  string   // truncated
    Message    string   // last_assistant_message (truncated)
}

type GenericMessagePayload struct {
    Title   string
    Body    string
    Urgency int // 0=low, 1=normal, 2=critical
}
```

### Migration path

The existing `NotifyEvent` (image-transfer specific) continues to work for `/clipboard/image`. The new `POST /notify` endpoint produces `ToolAttention` or `GenericMessage` envelopes. Both converge in the `RunNotifier` goroutine, but via **two channels**:

```go
notifyCh     chan NotifyEnvelope // buffered, cap=8, non-critical events (drop on full)
criticalCh   chan NotifyEnvelope // buffered, cap=4, critical events (blocking enqueue)
```

`RunNotifier` selects from both, with `criticalCh` checked first. This ensures `permission_prompt` and other urgency=critical events are never dropped even when non-critical notifications saturate the queue.

## Components

### 1. `POST /notify` Endpoint (`internal/daemon/server.go`)

```
POST /notify HTTP/1.1
Authorization: Bearer <notify-nonce>
Content-Type: application/json OR application/x-claude-hook

{...payload...}
```

**Authentication:** Uses a dedicated **notification nonce** (not the clipboard bearer token). Generated per `cc-clip connect`, written to `~/.cache/cc-clip/notify.nonce` on remote. This narrows the trust boundary: clipboard token holders cannot forge notifications, and notification nonce holders cannot read clipboard data. The nonce is a separate 32-byte random hex string with the same file permissions (chmod 600).

**Dual format detection:**

| Content-Type | Behavior |
|-------------|----------|
| `application/x-claude-hook` | Parse as Claude Code hook JSON. Daemon classifies internally (extracts type, title, body from hook fields). |
| `application/json` (default) | Parse as generic schema: `{"title": "...", "body": "...", "urgency": 0-2, "host": "..."}` |

**Response:** `204 No Content` on success, `400` on parse error, `401` on bad nonce.

**Visual distinction:** Notifications from `application/x-claude-hook` (verified hook source) show tool name and session context. Notifications from `application/json` (generic) are prefixed with `[unverified]` in the subtitle to distinguish them from authenticated Claude hook events. This prevents spoofing of "Tool approval needed" via generic endpoint.

### 2. Notification Classifier (`internal/daemon/classifier.go`)

Translates Claude Code hook JSON into structured notification data. Similar to cmux's `classifyClaudeNotification()`.

```go
func ClassifyHookPayload(hookType string, raw map[string]any) *ToolAttentionPayload
```

**Classification rules:**

| Hook type | Input field | Notification title | Urgency |
|-----------|------------|-------------------|---------|
| `notification` + `permission_prompt` | `title`, `body` | "Tool approval needed" | critical |
| `notification` + `idle_prompt` | `title`, `body` | "Claude is idle" | normal |
| `stop` + `stop_at_end_of_turn` | `last_assistant_message` | "Claude finished" | low |
| `stop` + other | `last_assistant_message` | "Claude stopped" | normal |

**Removed:** `pre_tool_use` was previously listed as a dedup-clear trigger, but V1 hook configuration only installs `Stop` and `Notification` hooks. Dedup logic must not depend on events that aren't wired. If PreToolUse is added in V2, dedup-clear can be revisited.

**`permission_prompt` is never suppressed by dedup/throttle AND routes to `criticalCh`** (blocking enqueue, never dropped).

### 3. Delivery Adapters (`internal/daemon/deliver_*.go`)

```go
// internal/daemon/deliver.go
type Deliverer interface {
    Deliver(ctx context.Context, env NotifyEnvelope) error
    Name() string
}

// DeliveryChain tries adapters in order; falls through on error.
type DeliveryChain struct {
    adapters []Deliverer
}

func (c *DeliveryChain) Deliver(ctx context.Context, env NotifyEnvelope) error {
    for _, a := range c.adapters {
        if err := a.Deliver(ctx, env); err == nil {
            return nil // first success wins
        }
        // log failure, try next adapter
    }
    return fmt.Errorf("all delivery adapters failed")
}

func BuildDeliveryChain() *DeliveryChain
```

**Chain delivery (not single-select):** Each adapter is tried in priority order. If cmux is detected but fails at runtime (e.g., no active surface, app not running), delivery falls through to macOS. This prevents "cmux binary exists but can't deliver" from swallowing the fallback.

| Priority | Adapter | Detection | Delivery mechanism | Fallthrough on |
|----------|---------|-----------|-------------------|----------------|
| 1 | cmux | `exec.LookPath("cmux")` at startup | `cmux notify --title ... --body ...` | non-zero exit or timeout |
| 2 | macOS | always available on darwin | terminal-notifier / osascript fallback | (terminal, never fails) |

**V2 planned:**

| Priority | Adapter | Detection | Delivery mechanism |
|----------|---------|-----------|-------------------|
| 0 (highest) | Terminal OSC | Find current terminal PTY | Write OSC 9/777 to PTY fd |

### 4. Dedup / Throttle (`internal/daemon/dedup.go`)

```go
type DedupKey struct {
    Source string
    Type   string
    Title  string
    BodyHash [16]byte // MD5 of body, for fast comparison
}

type DedupEntry struct {
    Key       DedupKey
    FirstSeen time.Time
    Count     int
}
```

**Rules:**
- Window: 10-15 seconds
- Same `DedupKey` within window: suppress, increment count
- Exception: `permission_prompt` notifications are **never** deduplicated
- Merged notification shows count: "Claude finished (x3)"

### 5. Remote: `cc-clip-hook.sh` (`internal/shim/hook_template.go`)

Bash script installed to `~/.local/bin/cc-clip-hook` on the remote. Claude Code hooks pipe JSON to stdin.

```bash
#!/usr/bin/env bash
# cc-clip-hook — Claude Code hook bridge
# Reads hook JSON from stdin, forwards to cc-clip daemon via tunnel

set -euo pipefail

_CC_CLIP_PORT="${CC_CLIP_PORT:-18339}"
_CC_CLIP_NONCE_FILE="${HOME}/.cache/cc-clip/notify.nonce"
_CC_CLIP_HOST_ALIAS="${CC_CLIP_HOST_ALIAS:-$(hostname -s)}"
_CC_CLIP_HEALTH_FILE="${HOME}/.cache/cc-clip/notify-health.log"

# Read notification nonce (dedicated, not clipboard token)
_nonce=""
if [ -f "$_CC_CLIP_NONCE_FILE" ]; then
    _nonce=$(head -1 "$_CC_CLIP_NONCE_FILE")
fi

# Read stdin (hook JSON)
_payload=$(cat)

# Inject host alias
_payload=$(echo "$_payload" | python3 -c "
import sys, json
d = json.load(sys.stdin)
d['_cc_clip_host'] = '${_CC_CLIP_HOST_ALIAS}'
json.dump(d, sys.stdout)
" 2>/dev/null || echo "$_payload")

# Forward to daemon (with health tracking)
_http_code=$(curl -sf -o /dev/null -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $_nonce" \
    -H "Content-Type: application/x-claude-hook" \
    -H "User-Agent: cc-clip-hook/0.1" \
    -d "$_payload" \
    "http://127.0.0.1:${_CC_CLIP_PORT}/notify" \
    2>/dev/null) || _http_code="000"

# Health tracking: log failures (visible via cc-clip status)
if [ "$_http_code" != "204" ] && [ "$_http_code" != "200" ]; then
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) FAIL http=$_http_code" >> "$_CC_CLIP_HEALTH_FILE" 2>/dev/null || true
fi

# Always exit 0 — hook failures must not block Claude Code
exit 0
```

**Health signal:** Failed deliveries are appended to `~/.cache/cc-clip/notify-health.log` on the remote. `cc-clip connect` can check this file and warn the user if recent failures are detected. This prevents the "silent disappearance" problem identified in adversarial review.

### 6. Remote: `cc-clip notify` Subcommand (`cmd/cc-clip/main.go`)

```bash
# Generic notification (explicit body)
cc-clip notify --title "Build complete" --body "All tests passed" --urgency 1

# Codex integration (reads JSON from first positional argument)
cc-clip notify --from-codex "$1"
```

**`--from-codex` mode:** Codex passes a JSON string as `$1` containing `{"last-assistant-message": "..."}`. The `--from-codex` flag parses this JSON, extracts `last-assistant-message` as body, sets title to "Codex", and POSTs to `/notify`. This is the single, documented Codex contract.

Reads notification nonce from `~/.cache/cc-clip/notify.nonce`, POSTs to `/notify` with generic JSON schema.

### 7. Claude Code Hook Configuration

`cc-clip connect` generates a paste-ready configuration snippet and prints it:

```
=== Claude Code Hook Setup ===
Add the following to your Claude Code settings on the remote:

claude config set --global hooks.Stop '[{"type":"command","command":"cc-clip-hook"}]'
claude config set --global hooks.Notification '[{"type":"command","command":"cc-clip-hook"}]'

Or add to ~/.claude/settings.json:
{
  "hooks": {
    "Stop": [{"type": "command", "command": "cc-clip-hook"}],
    "Notification": [{"type": "command", "command": "cc-clip-hook"}]
  }
}
```

V1 does NOT auto-modify Claude Code settings (user-controlled config, should not be silently changed).

### 8. Codex Auto-Configuration

**Design Note 1:** Codex notify injection is part of the regular `connect` flow, NOT exclusive to `--codex`. The `--codex` flag has existing semantics: it enables Xvfb/x11-bridge for clipboard support. A user who doesn't need X11 clipboard bridging but does use Codex should still get notifications. Codex config injection triggers when: (a) `~/.codex/` directory exists on remote AND (b) `--no-notify` is not set.

`cc-clip connect <host>` automatically writes to remote `~/.codex/config.toml` (if Codex is detected):

```toml
# cc-clip-managed — do not edit this line
notify = ["cc-clip", "notify", "--from-codex", "$1"]
```

Uses `# cc-clip-managed` guard (same pattern as PATH/DISPLAY markers). Detection: `test -d ~/.codex` on remote.

### 9. Connect Flow Changes

`cc-clip connect <host>` gains:
- **Step N: Generate notification nonce** — Create dedicated 32-byte random hex, write to remote `~/.cache/cc-clip/notify.nonce` (chmod 600), register on local daemon
- **Step N+1: Install hook script** — Upload `cc-clip-hook` to `~/.local/bin/`, chmod +x
- **Step N+2: Print Claude Code hook config** — Generate and display paste-ready snippet
- **Step N+3: Auto-configure Codex** — If `~/.codex/` exists on remote, inject notify setting
- **Step N+4: Health probe** — Send a test `POST /notify` with `{"title":"cc-clip","body":"notification bridge connected"}` to verify end-to-end delivery before declaring success
- **`--no-notify` flag** — Skip all notification-related steps (N through N+4)

Default: notification setup is enabled.

## Notification Examples

### Claude Code: Permission prompt
```
+-------------------------------------------+
| [venus] Claude Code            now        |
| Tool approval needed                      |
| Claude wants to Edit cmd/main.go          |
+-------------------------------------------+
```

### Claude Code: Task complete
```
+-------------------------------------------+
| [venus] Claude Code            now        |
| Claude finished                           |
| I've implemented the notification bridge  |
+-------------------------------------------+
```

### Codex: Task complete
```
+-------------------------------------------+
| [venus] Codex                  now        |
| Task complete                             |
| Added error handling to fetch module      |
+-------------------------------------------+
```

### Image transfer (existing, migrated)
```
+-------------------------------------------+
| cc-clip #3                     now        |
| a1b2c3d4 . 1920x1080 . PNG               |
| Image transferred                    [img]|
+-------------------------------------------+
```

## File Change List

| File | Type | Description |
|------|------|-------------|
| `internal/daemon/envelope.go` | New | NotifyEnvelope, NotifyKind, payload types |
| `internal/daemon/classifier.go` | New | ClassifyHookPayload — hook JSON to structured data |
| `internal/daemon/classifier_test.go` | New | Classification rules tests |
| `internal/daemon/dedup.go` | New | DedupKey, DedupEntry, window-based dedup |
| `internal/daemon/dedup_test.go` | New | Dedup window, permission_prompt exception |
| `internal/daemon/deliver.go` | New | Deliverer interface, DeliveryChain, BuildDeliveryChain |
| `internal/daemon/deliver_darwin.go` | New | macOS adapter (reuses terminal-notifier/osascript) |
| `internal/daemon/deliver_cmux.go` | New | cmux adapter (`cmux notify` command) |
| `internal/daemon/deliver_other.go` | New | No-op for non-darwin |
| `internal/daemon/deliver_test.go` | New | Adapter selection, delivery tests |
| `internal/daemon/server.go` | Modify | Add POST /notify handler, migrate notifyCh to NotifyEnvelope |
| `internal/daemon/notifier.go` | Modify | Adapt NotifyEvent → ImageTransferPayload bridge |
| `internal/daemon/notify_darwin.go` | Modify | Implement Deliverer for image_transfer kind |
| `internal/shim/hook_template.go` | New | cc-clip-hook bash script template |
| `internal/shim/hook_template_test.go` | New | Template rendering tests |
| `internal/token/nonce.go` | New | Notification nonce generation, validation, file I/O |
| `internal/token/nonce_test.go` | New | Nonce lifecycle tests |
| `cmd/cc-clip/main.go` | Modify | Add `notify` subcommand (with `--from-codex`), connect nonce + hook install + health probe, `--no-notify` flag |

## Implementation Order

```
Phase 1: Envelope model + classifier (pure logic)
  (1) internal/daemon/envelope.go
  (2) internal/daemon/classifier.go + classifier_test.go

Phase 2: Dedup engine (pure logic, no PreToolUse dependency)
  (3) internal/daemon/dedup.go + dedup_test.go

Phase 3: Notification nonce (trust boundary)
  (4) internal/token/nonce.go + nonce_test.go

Phase 4: Delivery chain
  (5) internal/daemon/deliver.go (interface + DeliveryChain)
  (6) internal/daemon/deliver_darwin.go (macOS, reuse existing notify_darwin logic)
  (7) internal/daemon/deliver_cmux.go (cmux adapter, fallthrough on failure)
  (8) internal/daemon/deliver_other.go (no-op)
  (9) internal/daemon/deliver_test.go (chain fallthrough tests)

Phase 5: Server integration (dual channel)
  (10) internal/daemon/server.go (POST /notify + notifyCh/criticalCh split + nonce auth)
  (11) internal/daemon/notifier.go (bridge NotifyEvent → NotifyEnvelope)

Phase 6: Remote components
  (12) internal/shim/hook_template.go + test (nonce-based auth + health logging)
  (13) cmd/cc-clip/main.go (notify subcommand with --from-codex + connect steps + --no-notify)

Phase 7: Codex auto-config (in regular connect, not --codex only)
  (14) Codex config.toml writer (detect ~/.codex/, inject with guard)

Phase 8: End-to-end verification
  (15) Health probe: connect sends test notification, verifies delivery
  (16) Local serve → connect → remote Claude Code hook → local notification
  (17) Local serve → connect → remote Codex notify → local notification
  (18) Chain fallthrough: cmux fails → macOS delivers
  (19) Critical channel: permission_prompt survives queue saturation
```

## Test Strategy

| Level | Coverage | Method |
|-------|----------|--------|
| Unit | Classifier: all hook types → correct title/urgency | Table-driven Go tests |
| Unit | Dedup: window merge, permission_prompt exception (no PreToolUse dep) | Time-controlled tests |
| Unit | DeliveryChain: cmux fail → macOS fallthrough | Mock adapters |
| Unit | Nonce: generation, validation, file I/O | Temp dir isolation |
| Unit | Hook template: nonce auth + health logging | String matching |
| Unit | Dual channel: critical enqueue blocks, non-critical drops | Channel capacity tests |
| Unit | `--from-codex` JSON parsing | Table-driven with valid/malformed inputs |
| Integration | POST /notify with nonce auth → notifyCh → mock deliverer | httptest |
| Integration | POST /notify with clipboard token → 401 rejected | httptest |
| Integration | Dual format: application/x-claude-hook vs application/json | httptest |
| Integration | Visual distinction: generic JSON gets [unverified] prefix | httptest + mock deliverer |
| Integration | Critical permission_prompt survives full notifyCh | Channel saturation test |
| Manual | cmux adapter: `cmux notify` shows in-app | Local cmux + curl |
| Manual | macOS adapter: terminal-notifier popup | Local daemon + curl |
| Manual | Health probe: connect displays test notification on success | Local connect |
| E2E | Full: SSH connect → remote Claude Code hook → local notification | SSH to test server |
| E2E | Full: SSH connect → remote Codex notify (--from-codex) → local notification | SSH to test server |
| E2E | Chain fallthrough: kill cmux → macOS fallback fires | Manual |
| E2E | Codex auto-detect: connect to host with ~/.codex/ → config injected | SSH to test server |

## E2E Verification Notes (2026-04-02)

Verified on venus (Ubuntu, SSH from macOS). Issues found and resolved during testing:

### Issues Found

1. **Hook script had no curl** — Venus had an old placeholder `cc-clip-hook` from a previous install that contained no curl logic. The new hook template must be deployed via `cc-clip connect` or manual install.

2. **curl had no timeout** — The hook template's curl command lacked `--connect-timeout` and `--max-time`. When the tunnel was down, hooks blocked for 2+ minutes, causing Claude Code's "running stop hooks" to stall. Fixed: `--connect-timeout 2 --max-time 5`.

3. **Stale sshd RemoteForward** — Old SSH sessions leave `sshd` child processes holding port 18339 in `CLOSE_WAIT` state. New SSH connections' RemoteForward silently fails because the port is already bound. Fix: `sudo kill $(sudo lsof -ti :18339)` before reconnecting. This is the same pitfall documented in CLAUDE.md "Known Pitfalls". The `cc-clip connect` flow should add a remote port cleanup step.

4. **Nonce not provisioned** — Plain `ssh venus` does not generate or register a notification nonce. Users must either use `cc-clip connect` (which handles this automatically) or manually generate and register a nonce. Consider auto-provisioning nonce during the notification setup step of `cc-clip connect`.

5. **Daemon binary mismatch** — The running daemon was an old binary without the `/notify` route. After rebuilding, the daemon must be restarted. The `/notify` endpoint hung instead of returning 404, which was confusing.

6. **User-Agent required for /register-nonce** — The `authMiddleware` requires `User-Agent: cc-clip/0.1`. Manual curl without this header gets 403, which is not obvious.

### Verified Working

| Test | Result |
|------|--------|
| Manual `echo ... \| cc-clip-hook` → local popup | PASS |
| Claude Code Stop hook → local popup ("Claude stopped") | PASS |
| Image paste Ctrl+V in Claude Code | PASS |
| Image paste notification (v0.5.0 path) | PASS |
| Dedup: 3x identical Stop in 12s → 1 popup | PASS |
| DeliveryChain: cmux fail → macOS fallback | PASS |
| Tunnel health: `curl /health` from venus | PASS |

### Not Triggered (By Design)

| Event | Why no notification |
|-------|-------------------|
| Claude waiting for user input | Not a hook event — normal interaction loop |
| Claude using tools (Bash/Edit) | `PreToolUse` hook not installed in V1 |

## Relationship to Existing Notification System

This design extends, not replaces, the v0.5.0 clipboard notification system:

| Aspect | v0.5.0 (clipboard) | v0.6.0 (bridge) |
|--------|--------------------|--------------------|
| Trigger | Image transfer via /clipboard/image | Hook event / CLI command via POST /notify |
| Data source | Daemon observes its own image serving | Remote tool pushes event to daemon |
| Direction | Local observation | Remote → local transport |
| Envelope kind | `image_transfer` | `tool_attention`, `generic_message` |
| Shared infra | notifyCh, RunNotifier, DarwinNotifier | Same channel, same goroutine, new adapters |

The `NotifyEnvelope` unifies both paths. `RunNotifier` consumes envelopes and dispatches to the appropriate `Deliverer` based on kind and available adapters.

## Future Roadmap

| Version | Feature | Notes |
|---------|---------|-------|
| v0.6.0 | Notification bridge V1 (this design) | macOS + cmux delivery, display-only |
| v0.6.1 | OSC delivery adapter | Write OSC 9/777 to detected terminal PTY |
| v0.7.0 | Action buttons | Bidirectional: Approve/Deny from notification → remote Claude Code |
| v0.7.1 | Web dashboard | Local web UI for notification history and action management |
| v0.8.0 | Windows/Linux local delivery | Toast notifications (Windows), libnotify (Linux) |
