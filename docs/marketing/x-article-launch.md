# I Built cc-clip Because Remote Image Paste Kept Breaking My Flow

![cc-clip demo](demo-quick.gif)

This is a launch article draft for `v0.9.0-beta.1`. It should read like a
maintainer explaining a workflow problem, not like a finished product campaign.

## The Problem

I spend a lot of time in remote AI coding sessions over SSH. The work is usually
text-heavy, but the moments that need images are important: UI bugs, screenshots,
diagrams, error dialogs, browser states, and design feedback.

The friction is small but repeated:

1. Take a screenshot locally.
2. SSH into a Linux host.
3. Open Claude Code, Codex CLI, or opencode.
4. Press paste.
5. The remote session cannot see the local image clipboard.

There are workarounds. You can save the file, upload it with `scp`, use a
terminal-specific trick, or avoid remote sessions when the task is visual. None
of those felt good enough when screenshots were part of the normal debugging
loop.

So I built `cc-clip`.

## What cc-clip Does

`cc-clip` is an SSH clipboard bridge for remote AI coding workflows.

At a high level:

```text
local clipboard -> local daemon -> SSH RemoteForward -> remote adapter -> agent
```

The exact remote adapter depends on the tool:

- Claude Code and opencode use an `xclip` / `wl-paste` shim.
- Codex CLI uses Xvfb plus a small X11 selection bridge.
- Notifications can travel back through the same local daemon path.
- Antigravity support today is notification setup only, not image paste.

The project does not patch Claude Code, Codex CLI, opencode, or Antigravity. It
tries to fit into the clipboard and notification paths those tools already use.

## Why The Implementation Is Narrow

I did not want a general clipboard sync service. The narrow goal is:

> make image paste less painful when the agent runs remotely over SSH.

That led to a few design choices.

First, the shim is conservative. It intercepts the image-paste calls and falls
back to the real system binary for other clipboard operations.

Second, the clipboard data is fetched on demand through the SSH tunnel. The
remote side does not need broad access to the local machine.

Third, Codex CLI gets a separate path. Codex reads the clipboard differently from
tools that shell out to `xclip`, so `cc-clip` can set up Xvfb and an X11 bridge
when you choose the Codex target.

Fourth, the setup is target-aware as of `v0.9.0-beta.1`:

```sh
cc-clip setup myserver              # Claude Code / xclip-wl-paste path
cc-clip setup myserver --codex      # Codex CLI only
cc-clip setup myserver --opencode   # opencode
cc-clip setup myserver --agy        # Antigravity notifications only
cc-clip setup myserver --all        # all current targets
```

The breaking change is worth spelling out: `--codex` now means Codex-only. If
you use Claude Code and Codex CLI on the same host, use `--all`.

## What Is New In v0.9.0-beta.1

This beta is mostly about making deployment safer as the number of supported
targets grows:

- target-aware setup for Claude Code, Codex CLI, opencode, and agy notifications
- deployment-state schema guards, so older binaries do not silently overwrite
  newer per-target state
- pinned install support through `CC_CLIP_VERSION`, useful for beta testing and
  rollback
- checksum verification for cross-architecture release downloads during remote
  deployment
- opencode notification setup through `connect --opencode` and `--all`

This is not the final stable `v0.9.0` release. It is a beta meant to catch real
SSH, shell, clipboard, and remote-host edge cases before the stable tag.

## How To Try The Beta

Because this is a prerelease, install it with an explicit version pin:

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=v0.9.0-beta.1 sh
cc-clip setup myserver
```

If the same host runs Claude Code and Codex CLI:

```sh
cc-clip setup myserver --all
```

If the host is opencode-only:

```sh
cc-clip setup myserver --opencode
```

For Codex CLI, the remote host needs Xvfb. `cc-clip` will try to install it when
passwordless `sudo` is available; otherwise it prints the command to run
manually.

## Boundaries

I want the support boundary to be clear:

- macOS -> remote Linux is the primary path.
- Windows support exists through `send` / `hotkey`, but it is still
  experimental.
- Antigravity is notification-only today.
- This is meant for SSH remote AI coding workflows, not general clipboard sync.
- If you paste images rarely, plain `scp` may be simpler.

Those limitations are not fine print. They are part of the beta.

## Feedback That Would Help

I would especially appreciate reports from people using:

- Codex CLI on headless Linux hosts
- opencode with X11 or Wayland clipboard paths
- SSH ControlMaster, jump hosts, or unusual shell startup files
- several remote hosts that need repeatable redeploy or rollback

Useful bug reports usually include:

- local OS
- remote OS
- target CLI
- setup command
- whether this is first install or upgrade
- `cc-clip doctor --host <host>` output with sensitive values redacted

## Links

- Repo: `https://github.com/ShunmeiCho/cc-clip`
- Beta release: `https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1`

If this matches a problem in your remote setup, I would be glad to hear about it.
If it does not fit your workflow, that feedback is useful too.
