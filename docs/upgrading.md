# Upgrading cc-clip

This page is for **users** of `cc-clip`. If you want to cut a new release,
see [release.md](release.md).

## How to find out a new version is available

cc-clip does not currently auto-check for updates. Pick whichever of these
fits your workflow:

- **Watch the repository on GitHub.** Open
  <https://github.com/ShunmeiCho/cc-clip>, click **Watch -> Custom -> Releases**.
  GitHub will email you whenever a tag ships.
- **Subscribe to the releases Atom feed** at
  <https://github.com/ShunmeiCho/cc-clip/releases.atom>.
- **Check your current version against the latest:**

  ```sh
  cc-clip --version
  curl -fsSL https://api.github.com/repos/ShunmeiCho/cc-clip/releases/latest \
    | grep '"tag_name"' | head -1 | cut -d'"' -f4
  ```

## What you should upgrade

When a new cc-clip version ships, every machine that runs cc-clip needs the
new binary:

| Where cc-clip runs | What it does | Needs upgrade when |
|---|---|---|
| Your local Mac or Linux laptop | Runs the HTTP daemon, hosts the clipboard | Always |
| Your Windows laptop | Runs the hotkey listener, sends files over SSH | Always |
| Each remote host you use with cc-clip | Hosts the xclip/wl-paste shim, the claude wrapper, and optionally x11-bridge/Xvfb | Whenever the local binary is updated (the remote side and local side share a protocol) |

The remote update is driven from your local machine via
`cc-clip connect <host>`. You do not SSH in and upgrade remotely by hand.

## macOS / Linux upgrade

### Option A: re-run the install script (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh
```

This fetches the archive for the latest `v*` tag, extracts it, replaces the
binary at `~/.local/bin/cc-clip`, clears macOS Gatekeeper quarantine, and
re-signs with `codesign --sign -`. The script is safe to re-run any time.

### Option B: manual download

Pick the archive matching your OS and arch from
<https://github.com/ShunmeiCho/cc-clip/releases/latest>, then:

```sh
V=0.6.1   # latest version without the v prefix
OS=darwin         # or linux
ARCH=arm64        # or amd64

cd /tmp
curl -fsSL -o cc-clip.tar.gz \
  "https://github.com/ShunmeiCho/cc-clip/releases/download/v${V}/cc-clip_${V}_${OS}_${ARCH}.tar.gz"
tar -xzf cc-clip.tar.gz
install -m 0755 cc-clip ~/.local/bin/cc-clip
# macOS only: clear quarantine + ad-hoc sign so Gatekeeper allows it
[ "$(uname -s)" = "Darwin" ] && xattr -cr ~/.local/bin/cc-clip \
  && codesign --force --sign - --identifier com.cc-clip.cli ~/.local/bin/cc-clip
```

### After the local binary is replaced

1. **Restart the clipboard daemon.** If you are running it as a user service:

    ```sh
    cc-clip service uninstall
    cc-clip service install
    cc-clip service status       # should show "running"
    ```

    If you run it in the foreground yourself, just stop the old process
    (`Ctrl+C`) and start it again with `cc-clip serve`.

2. **Confirm the upgraded daemon owns the port.** If another cc-clip bundle or
   old helper process is still listening on the daemon port, `connect` can talk
   to the wrong daemon and sync the wrong token to the remote.

    ```sh
    launchctl list | grep -i cc-clip
    lsof -nP -iTCP:18339 -sTCP:LISTEN
    curl -i -X POST http://127.0.0.1:18339/register-nonce
    ```

    Expected:
    - `launchctl` should show only the cc-clip daemon you intentionally run.
    - `lsof` should show the listener path/PID belongs to your upgraded
      `cc-clip` binary, not another bundled copy.
    - `/register-nonce` is a `POST`-only endpoint. Without an auth header it
      should return `401` or `403`. A `404` means an older daemon that does
      not know about notification nonces is answering; a `405` (from a GET)
      just means you forgot `-X POST`.

3. **Redeploy to every remote host** you use with cc-clip. This pushes the new
   binary to the remote and rebuilds the shim / hooks / x11-bridge entries:

    ```sh
    cc-clip connect myserver --force
    # add --codex if you use Codex CLI on that host
    cc-clip connect myserver --codex --force
    ```

    `--force` is important when upgrading: it bypasses the hash-based
    "binary unchanged, skipping" optimization so the new version actually
    lands on the remote.

4. **Verify**:

    ```sh
    cc-clip --version
    ssh myserver 'cc-clip --version'   # optional cross-check of the remote binary
    ```

## Windows upgrade

The install script does not support Windows. Upgrade is manual.

1. Stop any running cc-clip hotkey listener:

    ```powershell
    cc-clip hotkey --disable-autostart
    # or kill the tray icon process, or log out of the Windows session
    ```

2. Download the new zip from
   <https://github.com/ShunmeiCho/cc-clip/releases/latest> (pick
   `cc-clip_<version>_windows_amd64.zip` or `..._arm64.zip`).

3. Extract `cc-clip.exe` on top of your existing install location
   (typically `C:\Users\<you>\.local\bin`). Overwrite the old file.

4. Restart the hotkey listener:

    ```powershell
    cc-clip hotkey
    # or, to keep auto-start enabled:
    cc-clip hotkey myserver --enable-autostart
    cc-clip hotkey --status          # confirms the new version is registered
    ```

5. **Windows does not need `cc-clip connect`** — the Windows workflow talks
   to the remote over SSH/stdin directly, not via the xclip/wl-paste shim.
   There is no remote binary to redeploy for the Windows-only path. If you
   also use this remote from a Mac/Linux machine, run `cc-clip connect`
   from **that** machine after upgrading it.

## Pitfalls to know about

- **Daemon holding a stale binary:** macOS launchd keeps the old binary open
  until the service is restarted. If `cc-clip --version` on the CLI shows
  the new version but clipboard paste still behaves like the old one,
  `cc-clip service uninstall && cc-clip service install`.
- **Another daemon owns port 18339:** If `cc-clip connect` says the daemon is
  running but paste still fails, inspect `lsof -nP -iTCP:18339 -sTCP:LISTEN`.
  The process must be the upgraded cc-clip daemon you intend to use. Stop any
  old bundled copy before running `cc-clip connect <host> --force` again.
- **Remote cache says "unchanged":** `cc-clip connect` tracks the remote
  binary hash in `~/.cache/cc-clip/deploy-state.json` on the remote. If that
  file claims the binary is already current, `connect` will skip the upload.
  Use `--force` on upgrade runs.
- **Token vs binary upgrade:** If you only rotated the daemon token and did
  not change the binary, run `cc-clip connect myserver --token-only`
  instead — it syncs the token without a full redeploy.
- **macOS Gatekeeper:** the install script re-signs the binary after
  download. If you download manually you need to either run the two
  `xattr -cr` + `codesign ...` commands above, or Gatekeeper will refuse
  to execute the new binary.

## Rollback

cc-clip version upgrades are reversible — just install an older version the
same way.

- **Via install script, pinned to a specific tag:**

    ```sh
    # install.sh always fetches /releases/latest. To install a specific version
    # manually, use the Option B commands above with V= set to the version
    # you want. For example, V=0.5.0 downgrades to v0.5.0.
    ```

- **After downgrading, re-run `cc-clip connect <host> --force`** for each
  remote you use, so the remote side also goes back in sync.

If a release was published in error by the maintainer, they will mark it
`prerelease` on GitHub. `install.sh` will then skip it automatically and
fall back to the previous stable `v*` tag on the next run.
