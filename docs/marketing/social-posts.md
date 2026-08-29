# cc-clip Social Posts

Use these drafts for the first `v0.9.0-beta.1` promotion wave. Keep the tone
practical and humble. The most useful replies will come from people who
recognize the remote-SSH workflow problem, not from broad claims.

Canonical links:

- Repo: `https://github.com/ShunmeiCho/cc-clip`
- Beta release: `https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1`
- Demo GIF: `https://github.com/ShunmeiCho/cc-clip/blob/main/docs/marketing/demo-quick.gif`

Beta install:

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=v0.9.0-beta.1 sh
cc-clip setup myserver
```

## X / Twitter

### Short Launch Post

```text
I released cc-clip v0.9.0-beta.1.

It is meant to help paste screenshots into remote Claude Code, Codex CLI, and
opencode sessions over SSH, without patching those tools.

This is still a beta. Feedback from remote AI coding users would be very useful:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

### More Personal Version

```text
I built cc-clip because I kept losing image paste when moving from my local Mac
to remote AI coding sessions over SSH.

v0.9.0-beta.1 adds target-aware setup for Claude Code, Codex CLI, opencode, and
Antigravity notifications.

Beta feedback welcome:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

### Chinese Post

```text
我发布了 cc-clip v0.9.0-beta.1。

它面向的是一个很具体的问题：本地截图在 SSH 远程 Claude Code / Codex CLI /
opencode 会话里不好粘贴。

这还是 beta，不想夸大。更希望拿到真实远程开发环境里的反馈：
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

### Technical Thread

```text
1/ cc-clip started from a small frustration: local screenshots do not naturally
cross into remote AI coding sessions over SSH.

2/ If the agent runs on a Linux host, the remote clipboard is often empty. That
breaks UI debugging, visual feedback, and screenshot-heavy workflows.

3/ cc-clip bridges the gap with a local clipboard daemon, SSH RemoteForward, and
remote-side adapters.

4/ Claude Code and opencode use xclip / wl-paste shims. Codex CLI needs Xvfb
plus a small X11 bridge because it reads the clipboard differently.

5/ v0.9.0-beta.1 adds target-aware setup:
`--codex`, `--opencode`, `--agy`, and `--all`.

6/ Important breaking change: `--codex` now means Codex-only. Use `--all` for
Claude Code plus Codex on the same host.

7/ Boundaries: macOS -> Linux is the primary path, Windows is experimental, and
Antigravity is notification-only today.

8/ If you run AI coding agents over SSH, I would appreciate practical feedback:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## Hacker News

### Show HN

Title:

```text
Show HN: cc-clip - paste screenshots into remote AI coding sessions over SSH
```

Body:

```text
I built cc-clip because image paste kept breaking in my remote AI coding setup.

The workflow was simple: take a screenshot locally, SSH into a Linux machine,
open Claude Code or Codex CLI, press paste, and realize the remote session could
not see my local clipboard.

cc-clip tries to reduce that friction:

- local daemon reads the clipboard
- SSH RemoteForward carries it to the remote host
- xclip / wl-paste shims serve Claude Code and opencode
- Xvfb + a small X11 bridge serve Codex CLI
- notifications can come back through the same tunnel

The current release is v0.9.0-beta.1. It adds target-aware setup for Claude
Code, Codex CLI, opencode, and Antigravity notifications. It is still a beta,
and the primary tested path is macOS -> remote Linux. Windows support is
available through a separate experimental hotkey/SCP workflow.

Repo: https://github.com/ShunmeiCho/cc-clip
Beta release: https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1

If you use remote AI coding sessions over SSH, I would appreciate feedback on
where this works, where it breaks, and which setup assumptions I missed.
```

First comment:

```text
Extra context: I tried to keep this as a narrow SSH workflow tool, not a general
clipboard sync system.

The remote shim only intercepts the clipboard reads used for image paste and
falls back to the real system tool for other calls. Codex CLI needs a different
path because it reads X11 directly, so cc-clip can set up Xvfb and a small X11
selection bridge on the remote host.

Known limits: this beta may require manual Xvfb installation for Codex targets,
Windows is still experimental, and Antigravity support is notify-only today. I
am mainly looking for practical edge cases from people who use remote agents
daily.
```

## Reddit

Only post where the community rules allow open-source project sharing. If the
rules are unclear, ask the moderators or skip the subreddit.

### r/ClaudeAI

Title:

```text
I built a beta SSH clipboard bridge for remote Claude Code image paste
```

Body:

````markdown
I maintain an open-source tool called [cc-clip](https://github.com/ShunmeiCho/cc-clip).
I built it for a specific remote Claude Code problem: image paste breaks when
Claude runs on a Linux server over SSH but the screenshot is on your local
machine.

The current beta, `v0.9.0-beta.1`, can set up the Claude Code clipboard path and
also supports Codex CLI, opencode, and agent notifications.

The main path is macOS -> remote Linux:

- local clipboard daemon
- SSH RemoteForward
- remote xclip / wl-paste shim
- fallback to the real clipboard tool for non-image-paste calls

Beta install:

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=v0.9.0-beta.1 sh
cc-clip setup myserver
```

This is still beta software, and it is not a fit for every workflow. If you use
remote Claude Code over SSH and try it, I would really appreciate hearing what
worked and what did not.
````

### r/commandline

Title:

```text
cc-clip: SSH clipboard bridge for remote AI coding sessions
```

Body:

````markdown
I built [cc-clip](https://github.com/ShunmeiCho/cc-clip), a small Go CLI for a
remote-SSH clipboard problem: pasting local screenshots into AI coding agents
running on a Linux host.

The current beta has setup paths for Claude Code, Codex CLI, opencode, and
notifications. The interesting CLI pieces are:

- SSH RemoteForward transport
- xclip / wl-paste shim that falls back to the real binary
- Xvfb + X11 selection bridge for Codex CLI
- per-target setup flags in `v0.9.0-beta.1`
- checksum verification for release downloads and remote deployment

Main caveat: this is not a general clipboard-sync tool. It is aimed at remote AI
coding workflows, with macOS -> Linux as the primary path and Windows still
experimental.

Beta release:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

### r/LLMDevs

Title:

```text
Looking for feedback: beta SSH image-paste bridge for remote coding agents
```

Body:

```markdown
I am looking for feedback from people who run AI coding agents on remote Linux
hosts over SSH.

I maintain [cc-clip](https://github.com/ShunmeiCho/cc-clip), an open-source tool
that bridges local image paste into remote Claude Code, Codex CLI, and opencode
sessions. The new `v0.9.0-beta.1` release adds target-aware setup and stronger
deployment guards.

This is the rough model:

- Claude Code / opencode: xclip or wl-paste shim
- Codex CLI: Xvfb + X11 bridge
- notifications: agent hook / notify command -> SSH tunnel -> local daemon

I am not trying to claim this solves every remote clipboard setup. I would
mainly value reports about SSH config edge cases, headless Codex setups,
opencode clipboard behavior, and where the docs are confusing.

Beta release:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## GitHub Issue Replies

Use these only when directly relevant. Do not drop links into unrelated bug
threads.

### Remote Image Paste Issue

```text
I maintain a small open-source workaround for a related remote-SSH clipboard
problem, so I wanted to share it in case it helps anyone here:

https://github.com/ShunmeiCho/cc-clip

It is not an official integration, and it may not fit every setup. The current
beta is focused on macOS -> remote Linux workflows, with separate paths for
Claude Code/opencode (`xclip`/`wl-paste`) and Codex CLI (Xvfb + X11 bridge).

If this is too far from the issue topic, I am happy to remove the comment. If
anyone tries it, I would appreciate hearing where it works or breaks.
```

### opencode Clipboard Issue

```text
I maintain cc-clip, a small SSH clipboard bridge, and I have been testing it
against the same remote clipboard class of problems:

https://github.com/ShunmeiCho/cc-clip

For opencode, the current path uses the remote `xclip` / `wl-paste` calls rather
than an opencode-specific patch. It may help if your issue is specifically
"local image clipboard does not reach opencode over SSH".

This is a beta, not an official opencode integration. If the link is off-topic
here, I am happy to remove it.
```

### Antigravity Notification Context

```text
Small clarification from the cc-clip side: the current `--agy` target is
notification setup only. I do not want to imply Antigravity image paste support
until the clipboard transport is actually implemented and tested.

If you are evaluating cc-clip for Antigravity today, please treat it as a notify
bridge, not a clipboard bridge.
```

## GitHub Discussion Announcement

Title:

```text
v0.9.0-beta.1: target-aware setup for Claude Code, Codex CLI, opencode, and agy notifications
```

Body:

```markdown
`v0.9.0-beta.1` is available as a prerelease:

https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1

This beta focuses on target-aware setup:

```sh
cc-clip setup myserver              # Claude Code / xclip-wl-paste path
cc-clip setup myserver --codex      # Codex CLI only
cc-clip setup myserver --opencode   # opencode
cc-clip setup myserver --agy        # Antigravity notifications only
cc-clip setup myserver --all        # all current targets
```

The breaking change to notice: `--codex` now installs Codex support only. If you
use Claude Code and Codex CLI on the same host, use `--all`.

This is a beta. I would especially appreciate feedback on:

- Codex CLI on headless Linux hosts
- opencode on X11 or Wayland remotes
- SSH ControlMaster / jump-host / shell-startup edge cases
- rollback or redeploy flows across multiple hosts
````

## Direct Reply Templates

When someone gives useful feedback:

```text
Thanks, this is helpful. I especially appreciate the environment details because
SSH clipboard issues tend to hide in host-specific setup. I will turn this into
a concrete docs or test case if I can reproduce it.
```

When someone says they will wait:

```text
That is completely reasonable. This is still a beta, and I would rather people
wait than install it into a workflow where the support boundary is unclear.
```

When someone points out overclaiming:

```text
You are right. I should phrase that more carefully. The primary path is macOS to
remote Linux; Windows is experimental, and Antigravity is notify-only today.
Thanks for catching it.
```
