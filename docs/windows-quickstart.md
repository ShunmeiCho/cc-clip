# Windows Quick Start

This guide is the shortest path for using `cc-clip` on a **Windows local machine** with **remote Claude Code over SSH**.

If you are using macOS or Codex CLI, go back to the main [README](../README.md).

## What This Windows Workflow Does

On Windows, `cc-clip` does **not** rely on the remote `xclip` clipboard path.

Instead, it:

1. Reads the image from your Windows clipboard
2. Uploads it to the remote host over SSH
3. Pastes the remote file path into the active Claude Code terminal

This is the recommended path for:

- `Windows Terminal -> SSH -> tmux -> Claude Code`
- local Windows screenshot workflows
- users who want remote image paste without interfering with local Claude Code's native `Alt+V`

By default:

- **Local Claude Code** keeps using native `Alt+V`
- **Remote SSH Claude Code** uses `cc-clip`'s default hotkey: `Alt+Shift+V`

## Prerequisites

You need all of these on your Windows machine:

- Windows 10/11
- `ssh` in `PATH`
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

1. Download the latest Windows release from [GitHub Releases](https://github.com/ShunmeiCho/cc-clip/releases)
2. Pick the correct archive:
   - `cc-clip_<version>_windows_amd64.zip`
   - `cc-clip_<version>_windows_arm64.zip`
3. Extract `cc-clip.exe`
4. Put it in a stable directory such as `C:\Users\<you>\.local\bin`
5. Add that directory to your user `PATH`

Open a new terminal and verify:

```powershell
cc-clip --version
```

## Step 2: Configure the Remote Hotkey

Run this once:

```powershell
cc-clip hotkey myserver --enable-autostart
```

This does all of the following:

- saves `myserver` as your default remote host
- starts the background hotkey listener
- enables auto-start after Windows login
- uses the default remote-only hotkey `Alt+Shift+V`

Check the result:

```powershell
cc-clip hotkey --status
```

Expected output should include lines like:

```text
hotkey: running
hotkey: auto-start enabled
hotkey: default host myserver
hotkey: key alt+shift+v
```

## Step 3: Use It

Daily workflow:

1. Open `Windows Terminal`
2. SSH to the remote host
3. Enter `tmux`
4. Open remote Claude Code
5. Copy or screenshot an image on Windows
6. Focus the remote Claude Code terminal
7. Press `Alt+Shift+V`

`cc-clip` will:

- upload the image to the remote machine
- paste the remote file path into the active terminal
- restore your original clipboard image

> **Focus warning.** After you press `Alt+Shift+V`, `cc-clip` uploads the image, waits ~150 ms, then synthesizes `Ctrl+Shift+V` to paste the remote path into **whichever window is focused at that moment**. Do not switch windows during this short window — if the remote path lands in a password field, an IM input, or a browser URL bar, it has been delivered to the wrong trust boundary.
>
> If you need to switch away during upload, cancel the hotkey (focus a safe window first, or run `cc-clip hotkey --stop`) rather than letting the paste fire into an unintended target. Tracking: issue [#43](https://github.com/ShunmeiCho/cc-clip/issues/43) covers a foreground-window guard so future versions abort instead of pasting when focus has changed.

## Manual Fallback

If you do not want the background hotkey, run a one-shot paste instead:

```powershell
cc-clip send myserver --paste
```

Or, after you already saved a default host:

```powershell
cc-clip send --paste
```

If you already saved the image as a file, pass it directly:

```powershell
cc-clip send --paste C:\path\to\screenshot.png
```

## Change Host or Hotkey Later

Change the default remote host:

```powershell
cc-clip hotkey another-host --enable-autostart
```

Change the hotkey:

```powershell
cc-clip hotkey myserver --hotkey ctrl+alt+v --enable-autostart
```

View current settings:

```powershell
cc-clip hotkey --status
```

## Common Commands

Show status:

```powershell
cc-clip hotkey --status
```

Restart the background hotkey:

```powershell
cc-clip hotkey
```

Stop the background hotkey:

```powershell
cc-clip hotkey --stop
```

Disable auto-start:

```powershell
cc-clip hotkey --disable-autostart
```

## Troubleshooting

If the hotkey is configured but image paste still does not work:

1. Confirm `cc-clip hotkey --status` shows the expected host and hotkey
2. Confirm `ssh myserver` works from the same terminal
3. Try the manual fallback:

```powershell
cc-clip send myserver --paste
```

If that still fails, check the main troubleshooting guide:

- [Troubleshooting Guide](troubleshooting.md)
