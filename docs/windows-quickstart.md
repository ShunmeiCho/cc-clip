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

## Experimental: Direct Remote Clipboard

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

3. Open a new SSH session and check the remote can see the tunnel:

    ```sh
    bash -c 'echo >/dev/tcp/127.0.0.1/18339' && echo ok
    ```

4. Confirm the shim is first in `PATH`:

    ```sh
    which xclip
    head -1 "$(which xclip)"
    ```

If that still fails, check the main troubleshooting guide:

- [Troubleshooting Guide](troubleshooting.md)
