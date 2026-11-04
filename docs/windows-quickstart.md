# Windows Quick Start

This guide is the shortest path for using `cc-clip` on a **Windows local
machine** with remote Claude Code over SSH.

Release boundary:

- The default hotkey/send workflow is the released Windows workflow.
- The direct RemoteForward/shim workflow is not included in the latest stable
  release. Test it only from a source build of a commit that includes
  this feature, or from a later prerelease/release whose changelog explicitly mentions
  Windows direct clipboard support.

## Default: Hotkey Upload/Paste

The default Windows workflow keeps the older, explicit mechanism:

```text
Windows clipboard -> cc-clip hotkey/send -> SSH upload -> paste remote file path
```

This path does not depend on Windows Terminal exposing remote/tmux window
titles, and it does not require the remote app to call `xclip` or `wl-paste`.
It is the safer default across Windows Terminal, tmux, SSH clients, and Windows
10/11 variants.

## Prerequisites

You need all of these on your Windows machine:

- Windows 10/11
- PowerShell
- `ssh` and `scp` in `PATH`
- a working SSH host alias in `~/.ssh/config`

Example:

```ssh-config
Host myserver
    HostName 10.0.0.1
    User your-username
```

Verify it works:

```powershell
ssh myserver
exit
```

## Step 1: Install `cc-clip.exe`

For the released hotkey/send workflow, install a Windows release binary from
[GitHub Releases](https://github.com/ShunmeiCho/cc-clip/releases), put
`cc-clip.exe` in a stable directory such as
`%USERPROFILE%\.local\bin`, and add that directory to your user `PATH`.

If your release includes `scripts/install.ps1`, use the PowerShell installer:

```powershell
irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
```

It downloads the latest Windows zip, verifies `checksums.txt`, and installs
`cc-clip.exe` to `%USERPROFILE%\.local\bin` by default. Do not use the latest
release installer to test the direct RemoteForward/shim path; that feature is
not in the latest stable release.

If you want a different install directory:

```powershell
$env:CC_CLIP_INSTALL_DIR="$HOME\bin"; irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
```

If the installer tells you to add the directory to `PATH`, add it to your
**user** PATH and open a new terminal.

Verify:

```powershell
cc-clip --version
```

## Step 2: Start the Hotkey

Run this once:

```powershell
cc-clip hotkey myserver --enable-autostart
```

Then:

1. Copy or screenshot an image on Windows
2. Focus the remote Claude Code terminal for `myserver`
3. Press `Alt+Shift+V`

`cc-clip` uploads the image to `~/.cache/cc-clip/uploads` on `myserver`, puts
the remote image path on the Windows clipboard, sends `Ctrl+Shift+V`, and then
restores the original image clipboard.

Manual one-shot fallback:

```powershell
cc-clip send myserver --paste
```

The hotkey/send path is static: it sends to the configured host. If you use
several remote hosts at the same time, run an explicit one-shot command with
the host you want, or use separate hotkey configuration per workflow.

> **Focus guard.** The paste is delivered as a synthesized `Ctrl+Shift+V`, which
> goes to whichever window is focused when it fires, after the configured
> `--delay-ms`.
> `cc-clip` therefore records the focused window before writing the clipboard
> and re-checks it immediately before the keystroke. If focus moved, the paste
> is **aborted and nothing is typed**, so the remote path cannot land in a
> password field, a chat input, or a browser URL bar.
>
> An aborted paste reports `focus changed during paste` and is logged to
> `~/.cache/cc-clip/hotkey.log`. Re-focus the target window and press the
> hotkey again. The guard also aborts when Windows reports no foreground
> window at all, which happens briefly while a window is losing activation —
> retrying with the window focused is the fix.

### If nothing is pasted

The keystroke is delivered with `SendInput`, which Electron-based terminals
(Wave, Hyper, Tabby, VS Code's integrated terminal) accept. If a terminal still
refuses it, `cc-clip` now reports the refusal instead of claiming success — a
window running as administrator, for example, rejects input from a
non-elevated process.

The fallback does not stop the keystroke — it makes the keystroke optional.
`--no-restore` leaves the remote path on the clipboard afterwards, so if the
terminal did drop the synthetic `Ctrl+Shift+V`, your own `Ctrl+V` still pastes
the right thing:

```powershell
cc-clip hotkey myserver --no-restore
```

Without it the image is put back 150 ms after the keystroke, so a manual
`Ctrl+V` pastes the *image* — which in an Electron terminal becomes a local
`%TEMP%` path the remote agent cannot read. The setting is stored in
`hotkey.json` as `"no_restore": true`, so it survives the autostart launcher
and a reboot. The same flag has always been available on
`cc-clip send --paste`.

## Native Windows remote hosts (fork development build)

This fork also supports the hotkey/send workflow when the SSH server and the
coding agent run natively on Windows. It does not require Git Bash, WSL, Xvfb,
or a desktop login on the remote. This support is not in upstream v0.13.1.
The local and remote Windows machines must use the same architecture (amd64
or arm64) for this first implementation.

Build the development binary, then use it on your local Windows machine:

```powershell
go build -o .\dist\cc-clip-dev.exe .\cmd\cc-clip
.\dist\cc-clip-dev.exe send myserver .\screenshot.png
.\dist\cc-clip-dev.exe hotkey myserver
```

The first send probes the remote with Windows PowerShell, then deploys a
matching helper with SFTP. Images travel as raw bytes over SSH stdin; length
and SHA256 are checked before the final file is saved and before any path is
pasted. The helper is stored by binary hash under
`%LOCALAPPDATA%\cc-clip\bin`, and reused on later sends. Existing installed
`cc-clip.exe` files are left in place. PowerShell and SFTP must be available;
changing the SSH server's default shell is unnecessary.

Images are saved in `%LOCALAPPDATA%\cc-clip\uploads`. The receiver restricts
that directory and inherited file permissions to the SSH account and SYSTEM.
The default image limit is 20 MiB. Failed or incomplete transfers are removed.
Saved images are retained until you remove them. A Windows `--remote-dir` may
select a subdirectory of the managed uploads directory (relative paths are
resolved within that directory); paths outside that tree
are rejected to avoid changing permissions on an unrelated directory.

Press `Alt+Shift+V` with an image copied locally and the remote agent terminal
focused. The hotkey uploads the image and pastes its native Windows path.
Paths containing spaces are quoted. If synthetic paste does not work in your
terminal, use `hotkey myserver --no-restore` to keep the path on the clipboard
and paste it manually. This attaches images by file path. The native image
clipboard alternative is described below; Windows remote
`setup`/`connect`/`doctor --host` integration remains unavailable.

To start the development hotkey automatically at login, put its executable in
a stable location and run `cc-clip-dev.exe hotkey myserver --enable-autostart`.
Stop it with `cc-clip-dev.exe hotkey --disable-autostart`. For manual use, stop
it with `cc-clip-dev.exe hotkey --stop`.

### Native image clipboard bridge

Launch the remote agent through `bridge` from your local Windows terminal:

```powershell
.\dist\cc-clip-dev.exe bridge myserver -- codex.exe --no-daemon
```

If the agent is not in the remote PATH, an absolute executable path also works:

```powershell
.\dist\cc-clip-dev.exe bridge myserver -- C:\Users\you\.bun\bin\codex.exe --no-daemon
```

Copy an image locally, then use the agent's image paste shortcut. Codex accepts
`Alt+V`, which avoids terminal shortcuts that intercept `Ctrl+V`. The image
becomes a normal attachment in the remote agent. This workflow supports native
Windows executables that read PNG or DIBV5 clipboard images. Batch wrappers must
be launched through their shell explicitly, or replaced with their executable.

The wrapper starts a local loopback daemon, deploys the same build's helper
with SFTP, creates an authenticated SSH reverse tunnel on an automatically
selected remote loopback port, and starts the clipboard bridge and agent in
the same Windows window station. It does not require a remote desktop login,
WSL, or a change to SSH configuration. Existing configured forwards are excluded
from this connection. Windows OpenSSH must allow TCP forwarding.

Use `--no-daemon` with Codex versions that support a background server. A server
running in another Windows window station cannot read this SSH session's
clipboard. Start a new agent through `bridge`; an agent in an already-open SSH
session or a separate desktop session does not share the bridge's clipboard.

The bridge polls image metadata every 250 ms and fetches bytes when a native
consumer requests an image. It advertises PNG and CF_DIBV5, supports clipboard
updates, and rejects an image that changes while being fetched. Encoded images
are limited to 20 MiB and decoded pixels to 80 MiB. Text is not mirrored into
the remote clipboard. Run this against a trusted SSH account: the bridge's
authenticated local daemon retains the existing text and image read endpoints.

The token is unique to each invocation, stays out of command-line arguments,
and is stored remotely with permissions restricted to the SSH account and
SYSTEM. On exit, the local endpoint closes and the remote token file is removed
when reachable. Remote clipboard data owned by the bridge is cleared when the
agent exits. If the clipboard source stays unavailable for 10 seconds, the
bridge stops the agent. A lost connection can leave an expired token file, but
it cannot reconnect to a later invocation's endpoint.

To verify the native image path without launching an agent, copy an image and
run:

```powershell
.\dist\cc-clip-dev.exe bridge myserver --check
```

The diagnostic reports the remote window station, image dimensions, PNG SHA256,
and the two native formats read by an independent child process. If port
selection conflicts with a Windows excluded port range or another connection,
retry or specify `--remote-port 18339` before `--`. An unavailable port stops
startup instead of reusing another session's tunnel.

## Experimental: Direct Remote Clipboard on Linux

This section is for source builds and future explicit prereleases only. It is
not part of the latest stable release.

The experimental Windows direct path tries to match the macOS/Linux model:

```text
Windows clipboard -> local cc-clip daemon <- SSH RemoteForward <- remote shim <- Claude Code
```

In this mode, the remote `xclip` / `wl-paste` shim asks the local Windows
daemon for clipboard text or image data through the SSH tunnel. This avoids
choosing a host locally, but it depends on the remote app actually calling
`xclip` or `wl-paste` in a supported shape.

For source testing before a release, build the Windows binary and a remote
Linux binary from a commit that includes this feature:

```powershell
$env:GOOS="windows"; $env:GOARCH="amd64"; go build -o .\dist\cc-clip-windows.exe .\cmd\cc-clip
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o .\dist\cc-clip-linux-amd64 .\cmd\cc-clip
Remove-Item Env:GOOS, Env:GOARCH
```

Use the source-built Windows binary for the local daemon:

```powershell
.\dist\cc-clip-windows.exe service install
```

Make sure your SSH host has a RemoteForward:

```ssh-config
Host myserver
    RemoteForward 18339 127.0.0.1:18339
    ControlMaster no
    ControlPath none
```

Deploy the source-built remote binary:

```powershell
.\dist\cc-clip-windows.exe connect myserver --claude --force --local-bin .\dist\cc-clip-linux-amd64
```

After a release containing this feature is published, the normal setup path is:

```powershell
cc-clip setup myserver --claude
```

Then close old SSH sessions and open a fresh one:

```powershell
ssh myserver
```

Inside that remote shell:

```sh
which xclip
cc-clip status
```

`which xclip` should resolve to `~/.local/bin/xclip` when the shim is first in
`PATH`.

Security note: only run direct setup against remote hosts you trust. The daemon
token lets the remote shim request the current Windows clipboard **text and
image** content while the SSH tunnel is open. Images are capped at 20MB and text
at 1MB by default (`CC_CLIP_MAX_IMAGE_MB` / `CC_CLIP_MAX_TEXT_MB`), but the
token is still the access-control boundary.

### Experimental Stability Notes

The direct path is intentionally not the Windows default yet. It needs more
real-world coverage across:

- Windows 10 and Windows 11
- Windows Terminal, WezTerm, PuTTY, OpenSSH console, and tmux
- Snipping Tool, browser copied images, Office/Teams/WeChat-style rich
  clipboard data, and delayed-render clipboard providers
- remote tools that use different `xclip` / `wl-paste` argument patterns

If the direct path does not trigger, keep using the hotkey/send workflow above.

## Troubleshooting

If hotkey paste does not work:

1. Confirm the hotkey listener is running:

    ```powershell
    cc-clip hotkey --status
    ```

2. Try a one-shot paste with an explicit host:

    ```powershell
    cc-clip send myserver --paste
    ```

3. Confirm SSH upload works:

    ```powershell
    ssh myserver "mkdir -p ~/.cache/cc-clip/uploads && echo ok"
    ```

If experimental direct paste does not work:

1. Confirm the Windows daemon is running:

    ```powershell
    cc-clip service status
    ```

2. Confirm the SSH config contains the forward:

    ```powershell
    type $HOME\.ssh\config
    ```

3. Open a new SSH session and check the remote can reach the daemon through
   the tunnel:

    ```sh
    curl -sf http://127.0.0.1:18339/health
    ```

    A healthy tunnel answers `{"service":"cc-clip","status":"ok"}`. Do not use
    a bare TCP check such as `bash -c 'echo >/dev/tcp/127.0.0.1/18339'` — a
    stale `sshd` from an earlier session keeps the port open after its client
    is gone, so the handshake succeeds while nothing reaches the daemon. That
    is why `cc-clip connect` and `cc-clip doctor` ask the daemon to identify
    itself instead.

4. Confirm the shim is first in `PATH`:

    ```sh
    which xclip
    head -1 "$(which xclip)"
    ```

If that still fails, check the main troubleshooting guide:

- [Troubleshooting Guide](troubleshooting.md)
