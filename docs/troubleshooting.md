# Troubleshooting Guide

## Quick Diagnostics

```bash
cc-clip doctor --host myserver
```

## Step-by-Step Verification

If image paste isn't working, run these checks **in order** to isolate the problem:

```bash
# 1. Local: Is the daemon running?
curl -s http://127.0.0.1:18339/health
# Expected: {"service":"cc-clip","status":"ok","version":"..."}
# ("version" appears from v0.10.0; older daemons omit it)

# 2. Remote: Is the tunnel forwarding?
ssh myserver "curl -s http://127.0.0.1:18339/health"
# Expected: the same body as step 1

# 3. Remote: Is the shim taking priority over real xclip?
ssh myserver "which xclip"
# Expected: /home/<user>/.local/bin/xclip  (NOT /usr/bin/xclip)

# 4. Remote: Does the shim intercept correctly? (copy an image on Mac first)
ssh myserver 'CC_CLIP_DEBUG=1 xclip -selection clipboard -t TARGETS -o'
# Expected: image/png
```

---

## SSH ControlMaster Breaks RemoteForward

**Symptom:** `cc-clip connect` warns that the port is held on the remote but no cc-clip daemon answered, and `curl -s http://127.0.0.1:18339/health` hangs on the remote:

```
      WARNING: port 18339 is held on the remote, but no cc-clip daemon answered.
```

(Before v0.9.2 this same situation was misreported as `tunnel verified`, because the check only completed a TCP handshake.)

**Cause:** If you use SSH `ControlMaster auto` (connection multiplexing), the first SSH connection becomes the "master". All subsequent connections **reuse the master** — even if you later add `RemoteForward` to your config. The old master connection does not have the port forwarding, so the tunnel silently fails.

**Fix:** `cc-clip setup` automatically adds `ControlMaster no` for your host. If you configured SSH manually:

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

---

## Stale sshd Process Blocks RemoteForward

**Symptom:** one of these, on a host where paste used to work:

- `ssh myserver` shows `Warning: remote port forwarding failed for listen port 18339`;
- on the remote, `curl -s http://127.0.0.1:18339/health` connects and then hangs with no output;
- `cc-clip doctor --host myserver` reports `tunnel: [FAIL] port 18339 accepts connections but no cc-clip daemon answered`.

**Cause:** every `ssh myserver` takes the `RemoteForward` from your SSH config, and only the first one gets the port. If that session's client disappears without closing it cleanly (a network drop, a laptop sleep, a tool's background connection that reconnected), its `sshd` on the remote can keep the port while nothing on your machine answers. Later sessions, including new interactive ones, cannot take the port and SSH does not retry.

**Diagnosis:** `cc-clip doctor --host myserver` and `cc-clip connect` list your own `sshd` sessions on the remote with their start times when they see this, and mark the session the check itself ran under. You can also list them yourself; no root is needed:

```bash
# On the remote: your sshd sessions and when they started
ps -o pid,lstart,args -u "$(id -u)" | grep '[s]shd'

# On this machine: the ssh clients still running
ps -o pid,lstart,command -ax | grep '[s]sh'
```

The holder is the remote session with no matching client here. `lsof` and `ss -p` usually cannot show it to a normal user: `sshd` marks itself non-dumpable, so even your own session's sockets are hidden.

**Fix:**

```bash
# End the stale session. ClearAllForwardings keeps this ssh from taking the port itself.
ssh -o ClearAllForwardings=yes myserver 'kill <PID>'

# Then open a new session (reconnect the one you had) and check
ssh myserver
curl -s http://127.0.0.1:18339/health
# Expected: {"service":"cc-clip","status":"ok",...}
```

**Prevention:**

- If you administer the remote, set `ClientAliveInterval 30` and `ClientAliveCountMax 3` in its `sshd_config`, so `sshd` drops a session whose client is gone after about 90 seconds.
- cc-clip's own `connect`, `doctor` and `send` connections use `ClearAllForwardings=yes`, so they never take the port. Other tools that open background SSH connections to the same host alias do take it.
- `cc-clip tunnel run myserver` (experimental, #108) keeps the forward on a private SSH connection that reconnects by itself. It does not take over a port someone else already holds: end the stale holder first (Fix above), and comment out the host's `RemoteForward` in your SSH config while you use it, so other SSH connections do not take the port again.

---

## Auto-Starting the Daemon with SSH LocalCommand

If you skip the launchd service (`cc-clip service install`) and keep forgetting
to run `cc-clip serve` before SSHing, OpenSSH can start it for you. Add to the
host's block in `~/.ssh/config`:

```ssh-config
Host myserver
    RemoteForward 18339 127.0.0.1:18339
    PermitLocalCommand yes
    LocalCommand pgrep -f 'cc-clip serve' >/dev/null || (cc-clip serve >/dev/null 2>&1 &)
```

`LocalCommand` runs **on your local machine** after each successful connection
to that host; the `pgrep` guard makes it a no-op when the daemon is already
up. Notes:

- `PermitLocalCommand` is required — without it `LocalCommand` is silently
  ignored.
- This starts the daemon *after* the SSH connection is established, so the
  very first clipboard request may race the daemon's startup; a retry pastes
  fine. The launchd service does not have this race and remains the
  recommended path on macOS.
- Scope it to specific `Host` blocks rather than `Host *`: `LocalCommand`
  executes for every match, and a global one runs on every connection you
  make, including ones that have nothing to do with cc-clip.

---

## Managed Tunnel Service (Experimental)

Run `tunnel enable/status/restart/disable/auth/logs`
on the **local macOS machine running the daemon**, not on the Linux remote.
Start with `cc-clip tunnel status myserver` and
`cc-clip tunnel logs myserver -n 50`. `-f` follows the log until Ctrl-C.

`enable` requires an installed, healthy local daemon service. It checks SSH
authentication under launchd with `BatchMode=yes` and strict host-key checking
before starting the tunnel service. Use an explicit `IdentityFile` that works
without a prompt, or a keychain/agent
available to launchd. An interactive password or passphrase entered in a
terminal does not establish background authentication. cc-clip does not store
passwords or copy a shell's temporary `SSH_AUTH_SOCK` into the job.

If a service uses an unexpected port, check the saved settings with
`tunnel status myserver`. Existing services keep their saved port. For a new
service, priority is `--port`, the installed daemon's port, `CC_CLIP_PORT`, then 18339. Manage the service with the same SSH alias used for `enable`, as each
alias has its own job and saved state.

Find supervisor states such as `auth-required` in `last observed` from
`tunnel status myserver`, or in `managed tunnel state` entries from
`tunnel logs myserver`. The `reason` field and `managed tunnel recovery` log
entries provide details. The table also covers errors printed by service
commands and port differences reported by `status --port N`.

| State or diagnostic | Recovery |
|---|---|
| `auth-required` or `service-context SSH authentication failed` | Provide a key/agent usable in launchd and run `cc-clip tunnel auth myserver`. The command repeats the service-context check after foreground authentication |
| `host-key-error` or `service-context SSH host key verification failed` | Inspect `known_hosts`, verify the host's key and run `cc-clip tunnel auth myserver`. The background check requires a trusted key |
| `local-daemon-down` | Check `cc-clip service status` and the daemon at the saved port. The supervisor waits without repeatedly starting SSH |
| `port-conflict` | Find and end the connection holding the port, including another tool's persistent SSH connection. Manually comment out the host's legacy `RemoteForward` while trying managed mode. The supervisor retries without killing remote sshd |
| `remote-token-invalid` | Sync a valid local token with `cc-clip connect myserver --token-only`. The existing master/forward recovers on its next identity check |
| `identity-helper-missing` | Run `cc-clip connect myserver --force` to deploy the supported helper at `~/.local/bin/cc-clip` |
| `identity-endpoint-unavailable` | Check the daemon's version and identity initialization warning. Follow [identity repair](#tunnel-identity-unavailable) if needed |
| `identity-mismatch` | Confirm commands run on the local daemon machine and check its identity and configured port. Follow [identity repair](#tunnel-identity-unavailable) to recover the expected identity or explicitly bind a new one |
| `crash-loop` | Inspect the logs and fix the repeated failure, then run `cc-clip tunnel restart myserver` to clear valid runtime and start again |
| `config-error` | Inspect the logs, repair the configuration and restart explicitly |
| `configuration unknown`, `runtime: unknown`, or a configuration/schema error from a service command | Inspect the file named in the error and repair it before retrying. Restart validates schema and file permissions |
| `configuration` reports different saved and requested ports | `cc-clip tunnel status myserver --port N` only compares values. To change the saved port, explicitly disable, then enable with `--port N` and a matching daemon |

`status` reads the saved health result without probing the network. Check its
time and whether the supervisor is running before relying on `healthy`.
After sleep or a network change, allow time for reconnection and inspect the
logs before restarting. Authentication failures retry at low frequency. Remote
token/helper/endpoint failures keep the SSH connection while retrying checks.

`cc-clip tunnel disable myserver` stops the service and cancels automatic
startup. Configuration, runtime records and logs remain available for diagnosis.
To return to legacy forwarding, restore the manually commented-out
`RemoteForward` and open an SSH session. Automatic migration and
`restore-legacy` are not available yet.

---

## Tunnel Identity Unavailable

**Symptom:** The local daemon logs `WARN: tunnel identity unavailable`, and a
managed tunnel reports `identity-endpoint-unavailable`. The daemon continues
running. `/health` returns 200, `/tunnel/identity` returns 401 without a valid
token and 503 with one. Clipboard and notification requests keep their existing
authentication behavior. This warning disables only managed-tunnel identity
checks, so a managed tunnel cannot report `healthy`.

**Corrupt identity file:** The warning names the actual `instance-id` path and
the repair steps. The default is `~/.cache/cc-clip/instance-id`. If
`CC_CLIP_TOKEN_DIR` is set for the daemon, it is `<CC_CLIP_TOKEN_DIR>/instance-id`.
The file must contain a 64-character hexadecimal installation ID. cc-clip leaves
empty or invalid files untouched rather than silently changing the identity that
existing tunnel configurations expect.

Repair it on the **local machine running the daemon** with the following steps.

1. Stop all foreground managed tunnels and disable each enabled service with
   `cc-clip tunnel disable <host>`. Record each host's port before disabling it.
2. Stop the daemon. For the macOS daemon service, run
   `cc-clip service uninstall`. For a foreground daemon, stop `cc-clip serve`.
   Pause any other launcher that would restart it during the repair.
3. Restore the original valid identity from a trusted backup if available. If
   there is no valid backup, move **only the damaged `instance-id` file** to a
   backup path that does not already exist. Keep `session.token`, notification
   credentials and tunnel state files. This deliberately creates a new
   installation identity on the next daemon start.
4. Keep the identity directory private (`0700`) and the restored file `0600`.
   Restart the daemon with its original port and state-directory settings. On
   macOS, use `cc-clip service install --port <original-port>`. For foreground
   use, restart `cc-clip serve --port <original-port>`.
5. If a new identity was created, explicitly re-enable each disabled service
   with `cc-clip tunnel enable <host> --port <original-port>`, or restart each
   foreground tunnel with `cc-clip tunnel run <host> --port <original-port> --reset`.
   These operations update the expected identity. A plain tunnel restart keeps
   the saved identity. If the original identity was restored, re-enable services
   or resume foreground tunnels normally. Verify each tunnel's health.

**Filesystem without hard links:** Creating a new `instance-id` requires
hard-link support. exFAT and some FUSE mounts reject that operation, leaving
identity unavailable. A valid existing identity can still be loaded. The
warning names the destination file and suggests checking directory permissions
and hard-link support.

Use a private token/identity directory on a filesystem that supports hard links.
When relocating it, stop the daemon and managed tunnels first, preserve
`session.token` and any valid `instance-id`, and keep directory/file permissions
at `0700`/`0600`. Configure `CC_CLIP_TOKEN_DIR` consistently for the daemon and
local commands. Daemon and tunnel services need that setting in their own environments.
Exporting it in a terminal alone does not change an existing LaunchAgent. If
identity initialization still fails, the daemon continues in the degraded mode
described above.

---

## Token Expired or Invalid

**Symptom:** "fetch type failed" or "token invalid" / "401" in shim debug logs. Image paste silently falls back to the remote (empty) clipboard.

**Cause:** Token TTL (30 days) expired due to prolonged inactivity, or daemon restarted and generated a new token.

**Fix:**

```bash
# Re-sync token without re-uploading the binary
cc-clip connect myserver --token-only
```

Clipboard requests retain **sliding expiration**, renewing when less than half
of the configured TTL remains. Identity checks, `/health`, SSH connectivity and
notification requests do not renew this token. A continuously running managed
tunnel can therefore reach token expiry if it only monitors health or delivers
notifications. Notification nonces have their own lifecycle.

If the local daemon session is also expired, token-only synchronization cannot
repair it. Stop/restart the daemon using its original port, token directory and
TTL settings. An expired token is replaced on startup. For a macOS daemon
service, record its port/settings, then use `cc-clip service uninstall` and
`cc-clip service install --port <original-port>`. Preserve any custom service
environment. For foreground use, stop and restart `cc-clip serve` with the same
settings. Keep `instance-id` unchanged, then sync the new token to each host
with `connect <host> --token-only`. Remote-token rejection alone does not require
restarting a healthy SSH master.

`cc-clip serve --rotate-token` explicitly generates a new token. It must start
after the old daemon stops. Sync the result to remote hosts afterwards.

---

## `cc-clip update` Fails with "API rate limit exceeded"

**Symptom:** `cc-clip update` (or `install.sh`) stops with `failed to query latest release: GitHub API returned 403: {"message":"API rate limit exceeded for <your IP>..."}` or `could not determine latest version`.

**Cause:** Looking up the latest release uses the GitHub API, which allows 60 unauthenticated requests per hour per public IP. Behind a university or company NAT that budget is shared with everyone on the network, so it can be used up without you running `update` at all (#170).

**Fix:** In releases after v0.12.2, `update` and `install.sh` fall back to the `github.com/.../releases/latest` redirect, which is not rate-limited, so this should no longer stop them. On an older cc-clip, or if both lookups fail, skip the lookup or authenticate it:

```bash
# Name the version (see https://github.com/ShunmeiCho/cc-clip/releases); no API call is made
cc-clip update --to vX.Y.Z

# Or send a token: 5000 requests/hour instead of 60
GH_TOKEN=$(gh auth token) cc-clip update

# Fresh install: pin the version for install.sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=vX.Y.Z sh
```

`update` also prints when the API limit resets. Downloading the release archive itself is not affected by this limit.

---

## Launchd Daemon Returns "empty" for Image Clipboard

**Symptom:** `cc-clip service install` is running, but `/clipboard/type` returns `{"type":"empty"}` even when you have an image in your Mac clipboard. Running `cc-clip serve` in the foreground works correctly.

**Cause:** macOS `launchd` does not source your shell profile, so `PATH` doesn't include Homebrew directories (`/opt/homebrew/bin` on Apple Silicon, `/usr/local/bin` on Intel). The daemon can't find `pngpaste`.

**Fix:** Reinstall the service to regenerate the plist with correct PATH:

```bash
cc-clip service uninstall
cc-clip service install
```

---

## Empty Image Data (API Error 400)

**Symptom:** Claude Code returns `API Error: 400 — image cannot be empty`. The conversation becomes corrupted and all subsequent image pastes fail in the same session.

**Cause:** A race condition where the clipboard content changes between the TARGETS check and the image fetch. The shim outputs empty data, and Claude Code sends an empty base64 image to the API.

**Fix:**

1. In Claude Code, run `/clear` or start a new session (the old conversation is corrupted)
2. Update to the latest cc-clip and re-run `cc-clip connect myserver`

---

## `~/.local/bin` Not in PATH

**Symptom:** `cc-clip connect` shows WARNING: `'which xclip' resolves to /usr/bin/xclip, not ~/.local/bin/xclip`.

**Cause:** The shim is installed to `~/.local/bin/` but it's not first in PATH, so the system uses `/usr/bin/xclip` instead.

**Fix:** `cc-clip connect` auto-detects your remote shell and prepends a PATH marker to the appropriate rc file. If auto-fix didn't work, add manually:

```bash
# Add to the TOP of ~/.bashrc (before the interactive guard)
export PATH="$HOME/.local/bin:$PATH"
```

Verify with `which xclip` — it should point to `~/.local/bin/xclip`.

---

## No Image in Clipboard

**Symptom:** Shim returns `image/png` for TARGETS but Claude Code says "No image found in clipboard".

**Cause:** You may not have an image in your Mac clipboard.

**Fix:** Copy an image on your Mac first:
- **Screenshot to clipboard:** `Cmd + Shift + Ctrl + 4` (select area) or `Cmd + Shift + Ctrl + 3` (full screen)
- **Copy from an app:** Right-click an image → Copy Image

## Clipboard-History Manager "Paste" Button Bypasses cc-clip

**Symptom:** You copied an image, but pasting it into remote Claude Code / Codex
via a clipboard-history manager's **"Paste to <terminal>"** button inserts the
image's file *path* as text instead of the image. cc-clip never seems to fire.

**Cause:** This is a usage gotcha, not a cc-clip failure. Clipboard-history
managers (Raycast, Maccy, Paste) "Paste to <app>" buttons inject the file path as
text — they do **not** trigger the agent's own clipboard read. cc-clip only
serves the clipboard when the agent itself reads it, via the xclip shim (Claude
Code) or arboard (Codex). A manager that types a path on your behalf goes around
that read entirely.

**Fix:** Paste with the **agent's own `Ctrl+V`**, after copying the image as
**raw image data** (not a file reference):

- Take a fresh screenshot to the clipboard (`Cmd + Shift + Ctrl + 4` /
  `Cmd + Shift + Ctrl + 3`), or right-click an image → **Copy Image**.
- In the remote Claude Code / Codex session, press `Ctrl+V` directly.

Do **not** use the clipboard manager's paste button for images — it never hands
the raw image to the agent, so cc-clip is never asked for it.

## Setup Fails: "killed" During Re-Deployment

**Symptom:** `cc-clip setup` was working before, but now shows `zsh: killed` when re-running.

**Cause:** The launchd service is running the old binary. Replacing the binary while the daemon holds it open can cause conflicts.

**Fix:**

```bash
cc-clip service uninstall
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh
cc-clip setup myserver
```
