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
| Each remote host you use with cc-clip | Hosts the xclip/wl-paste shim, Claude Code hook config, and optionally x11-bridge/Xvfb | Whenever the local binary is updated (the remote side and local side share a protocol) |

The remote update is driven from your local machine via
`cc-clip connect <host>`. You do not SSH in and upgrade remotely by hand.

## Upgrading from v0.8.x to v0.9.0

v0.9.0 introduces **per-target setup**. One behavior change matters when you
upgrade an existing host:

- **`--codex` is now Codex-only.** In v0.8.x, `cc-clip connect <host> --codex`
  installed Codex support **on top of** the Claude shim. In v0.9.0 it installs
  **Codex only** (Xvfb + x11-bridge, no Claude shim). If you used `--codex` to
  get both, switch to `--all`:

    ```sh
    # v0.8.x (old): --codex meant Claude shim + Codex
    cc-clip connect myserver --codex --force
    # v0.9.0 (new): use --all for the same Claude + Codex result
    cc-clip connect myserver --all --force
    ```

- **New targets:** `--opencode`, `--agy` (Antigravity), and an explicit
  `--claude`. `--all` selects every target.

Otherwise the upgrade is the normal forward path below: update the local binary,
then redeploy each remote with `--force`. Downgrading back across the v0.9.0
boundary is **not lossless** — see
[Cross-v0.9 downgrade](#cross-v09-downgrade-pre-v090) first.

## macOS / Linux upgrade

### Option A: `cc-clip update` (recommended, cc-clip 0.6.2+)

```sh
cc-clip update --check      # see what's newer without touching anything
cc-clip update              # download, verify checksum, swap binary, restart daemon
```

This is the native one-shot path. Compared to re-running `install.sh`, it
additionally:

- Detects port-18339 conflicts *before* downloading. If another process
  (a bundled `cc-clip` from a different tool, a forgotten stray daemon) is
  holding the port, the update aborts with an actionable error instead of
  silently leaving you with a mismatched token on remotes.
- Verifies the staged archive's `--version` before swapping the binary, so
  a mislabeled release cannot overwrite a working install.
- Rolls the binary and launchd plist back automatically if
  `GET /health` on the daemon port does not come up on the new binary
  within 10s of the swap.

`--force` re-installs even when already at the target version and bypasses
the pre-download conflict check. `--to v0.6.0` pins a specific release
instead of the latest (handy for rollback).

Not yet supported: Windows (use the Windows installer path below), and
systemd-based Linux services (the updater prints a reminder instead of
restarting the service).

### Option B: re-run the install script

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh
```

This fetches the archive for the latest `v*` tag, extracts it, replaces the
binary at `~/.local/bin/cc-clip`, clears macOS Gatekeeper quarantine, and
re-signs with `codesign --sign -`. Safe to re-run any time; useful when
`cc-clip update` is not yet available on your machine (first-time install,
or pre-0.6.2 cc-clip).

### Option C: manual download

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
    # Claude Code only (default):
    cc-clip connect myserver --force
    # Claude Code + Codex CLI on that host (v0.9.0: --codex alone is Codex-only):
    cc-clip connect myserver --all --force
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

`cc-clip update` does not support Windows yet. Use the manual zip path for the
latest stable release. The PowerShell installer applies to releases that include
`scripts/install.ps1`; do not use the latest release installer to test
unreleased direct RemoteForward/shim behavior.

1. Stop any running cc-clip hotkey listener:

    ```powershell
    cc-clip hotkey --disable-autostart
    # or kill the tray icon process, or log out of the Windows session
    ```

2. For a release that includes the Windows installer, run:

    ```powershell
    irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
    ```

    It downloads `cc-clip_<version>_windows_<arch>.zip`, verifies
    `checksums.txt`, and replaces `cc-clip.exe` in
    `%USERPROFILE%\.local\bin` by default. If the feature you want is only in
    an unreleased branch, build from source or wait for an explicit prerelease
    tag instead of using the latest stable release.

    To install a specific version for rollback or pinning:

    ```powershell
    $env:CC_CLIP_VERSION="v0.5.0"; irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
    ```

    If you installed cc-clip somewhere else, pass that directory:

    ```powershell
    $env:CC_CLIP_INSTALL_DIR="$HOME\bin"; irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
    ```

3. Manual fallback: download the new zip from
   <https://github.com/ShunmeiCho/cc-clip/releases/latest> (pick
   `cc-clip_<version>_windows_amd64.zip` or `..._arm64.zip`), then extract
   `cc-clip.exe` on top of your existing install location.

4. Restart whichever Windows component you use.

    For the default hotkey/send workflow:

    ```powershell
    cc-clip hotkey
    # or, to keep auto-start enabled:
    cc-clip hotkey myserver --enable-autostart
    cc-clip hotkey --status          # confirms the new version is registered
    ```

    For the experimental direct shim workflow, only after installing a source
    build or a release that explicitly contains Windows direct clipboard
    support:

    ```powershell
    cc-clip service uninstall
    cc-clip service install
    cc-clip service status
    ```

5. If you opted into the experimental direct shim workflow from a source build
   or explicit prerelease, redeploy to every remote host from Windows so the
   remote binary/shim matches the upgraded local daemon:

    ```powershell
    cc-clip connect myserver --claude --force
    ```

   If you use only the default hotkey/send workflow (`cc-clip send --paste` /
   `cc-clip hotkey`) and never installed the remote shim from Windows, there
   is no remote binary to redeploy for that path.

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
  binary hash in `~/.cache/cc-clip/deploy.json` on the remote. If that
  file claims the binary is already current, `connect` will skip the upload.
  Use `--force` on upgrade runs. (`deploy.json` also carries a
  `schema_version` that drives the forward-downgrade guard — see
  [Rollback](#rollback).)
- **Token vs binary upgrade:** If you only rotated the daemon token and did
  not change the binary, run `cc-clip connect myserver --token-only`
  instead — it syncs the token without a full redeploy.
- **macOS Gatekeeper:** the install script re-signs the binary after
  download. If you download manually you need to either run the two
  `xattr -cr` + `codesign ...` commands above, or Gatekeeper will refuse
  to execute the new binary.

## Rollback

There are two flavours of rollback, and they are **not** equally safe:

- **Same-generation rollback** (for example `v0.9.1 -> v0.9.0`): the normal,
  lossless path. Both binaries speak the same deploy-state schema and know the
  same deployment targets (`--codex`, `--opencode`, `--agy`), so re-running
  `connect --force` simply re-syncs the older binary. This is what the runbook
  below covers.
- **Cross-v0.9 downgrade** (to a pre-`v0.9.0` binary): **not lossless** — see
  the [Cross-v0.9 downgrade](#cross-v09-downgrade-pre-v090) subsection before
  you attempt it.

### Pin a version (forward or backward)

- **`cc-clip update --to v0.5.0`** (cc-clip 0.6.2+): same semantics as a
  forward upgrade — checksum-verify, swap, restart, verify — but against
  the pinned release tag instead of `/latest`.

- **Via install script, pinned with `CC_CLIP_VERSION`** (curl-install rollback
  channel, mirrors `cc-clip update --to`): `install.sh` fetches
  `/releases/latest` by default, but if `CC_CLIP_VERSION` is set it installs
  that exact tag instead. This is the recommended one-liner for machines that
  do not yet have `cc-clip update`:

    ```sh
    curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh \
      | CC_CLIP_VERSION=v0.5.0 sh
    ```

    The env var must be set on the **right-hand side of the pipe** (the `sh` that
    runs the script), not on `curl`. `CC_CLIP_VERSION=v0.5.0 curl ... | sh` would
    export the pin only into the `curl` process and the piped `sh` would still
    install `/latest`.

  The value must be a full tag (for example `v0.5.0`); a missing `v` prefix is
  accepted. An invalid value aborts the install with an actionable error.

- **Via install script, the manual way:** you can also skip `CC_CLIP_VERSION`
  and use the Option C manual-download commands above with `V=` set to the
  version you want (for example `V=0.5.0` downgrades to `v0.5.0`).

- **Windows PowerShell installer:** same pinning model, using the Windows zip
  asset and `Get-FileHash` verification:

    ```powershell
    $env:CC_CLIP_VERSION="v0.5.0"; irm https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.ps1 | iex
    ```

### Rollback runbook

For a **same-generation** rollback, after the local binary is replaced:

1. **Restart the daemon** (`cc-clip service uninstall && cc-clip service
   install`) so launchd stops holding the old binary — same as a forward
   upgrade.

2. **List the remotes that need re-syncing.** The local host registry knows
   every machine you have deployed to and the version it last received:

    ```sh
    cc-clip hosts list
    # HOST      VERSION  CODEX  LAST CONNECTED
    # myserver  0.9.1    no     2026-06-01T10:22:04+09:00
    # venus     0.9.1    yes    2026-05-30T18:05:11+09:00
    ```

3. **Re-run `cc-clip connect <host> --force`** for each remote in that list, so
   the remote side goes back in sync with the downgraded local binary. `--force`
   bypasses the hash-based "binary unchanged, skipping" optimization.

    Note the new guard (v0.9.0+): if the remote's `deploy.json` was last written
    by a **newer** cc-clip than the one you just rolled back to, `connect`
    **refuses** to overwrite it and tells you to upgrade this cc-clip or pass
    `--force`. This protects you from a stale local binary silently clobbering a
    remote that a newer machine deployed. It is a *forward* guard only — see the
    caveat below for what it does and does not cover.

### Cross-v0.9 downgrade (pre-v0.9.0)

Downgrading a remote to a **pre-`v0.9.0`** binary is **not lossless**, and there
is no automatic guard that will stop you. Two things go wrong:

- A pre-`v0.9.0` binary does not understand the v0.9 deployment-target model
  (`--opencode`, `--agy`, and the per-adapter `Notify.Adapters` map in
  `deploy.json`). It only knows the original Claude-Code shim + hook layout.
- Being a *pre-guard* binary, it has no `schema_version` awareness. The
  forward-downgrade guard described above only exists in binaries that **ship**
  it (v0.9.0 and later). An older binary will happily **overwrite**
  `~/.cache/cc-clip/deploy.json` on the remote, dropping the `Adapters` map and
  the `agy-notify` / `opencode-notify` entries.

So do **not** rely on a fail-closed here — the protection is forward-only by
design. If you must cross the v0.9 boundary downward, expect to clean up and
redeploy those adapters by hand:

1. **Manually tear down the v0.9-only adapters** on the remote you are about to
   downgrade, so no orphaned plugin / hook entries are left behind. There is **no**
   `cc-clip uninstall <host> --opencode` / `--agy` command today — symmetric
   uninstall for those targets is not yet implemented (`cc-clip uninstall`
   currently supports only `--codex` and `--host`). Clean them up over SSH by hand,
   only for whichever adapters you actually enabled:

    ```sh
    # opencode notify plugin (the dropped .js is removed by hand):
    ssh <host> 'rm -f "$HOME/.config/opencode/plugins/cc-clip-notify.js"'

    # Antigravity (agy) notify plugin (use agy's own uninstall on the remote;
    # agy's managed plugins dir is version-specific, so let the CLI find it):
    ssh <host> 'agy plugin uninstall cc-clip-notify'
    ```

    (Future cc-clip versions may add `cc-clip uninstall --opencode` / `--agy` to
    automate this; until then the manual cleanup above is the supported path.)
2. Roll the remote back by deploying the pre-v0.9 binary from the older local
   cc-clip (`connect --force`), accepting that `deploy.json` will be rewritten
   in the old format.
3. When you later move forward across v0.9 again, re-enable the adapters you
   want (`connect <host> --opencode` / `--agy` / `--all`); v0.9.0+ will
   re-stamp `deploy.json` with the current `schema_version`.

If a release was published in error by the maintainer, they will mark it
`prerelease` on GitHub. `install.sh` will then skip it automatically and
fall back to the previous stable `v*` tag on the next run.
