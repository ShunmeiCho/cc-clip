# cc-clip v0.9.0-beta.1 Launch Kit

This is the source of truth for the first public beta promotion wave.

Directory index: `docs/marketing/README.md`.

The tone should be modest, specific, and easy to respond to. The goal is not to
sound like a product launch with finished guarantees. The goal is to tell the
right users what problem this targets, invite careful feedback, and respect the
communities where the project is shared.

Before any public action, check
`docs/marketing/launch-readiness-checklist.md`.

For public-action approvals and exact commands, use
`docs/marketing/promotion-approval-runbook.md`.

For GitHub issue target selection, use
`docs/marketing/community-outreach-targets.md`.

For post-launch replies and feedback triage, use
`docs/marketing/post-launch-response-playbook.md`.

For Chinese-language posts, private asks, and community replies, use
`docs/marketing/chinese-launch-kit.md`.

## Verified Facts

Current evidence checked on 2026-06-04:

- GitHub release: `v0.9.0-beta.1`
- Release URL: `https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1`
- Release state: published prerelease, not draft
- Release workflow: completed successfully for commit `291b88c11b15d18cbcaad1d4dfdbfaca55f991f0`
- Published assets: macOS, Linux, Windows archives for amd64/arm64, plus `checksums.txt`
- README support boundary:
  - macOS -> Linux image paste is the primary stable path
  - Windows -> Linux uses the experimental `send` / `hotkey` path
  - Claude Code and opencode use the xclip / wl-paste shim path
  - Codex CLI uses Xvfb + x11-bridge and may require remote `sudo` or manual Xvfb installation
  - Antigravity is notify-only today; clipboard transport is still pending

## Launch Goals

Priority order:

1. Attract thoughtful technical feedback from remote AI coding users.
2. Help people who already hit this clipboard problem discover the project.
3. Earn GitHub stars and early installs naturally, without pushing too hard.

This is a beta launch. It is fine if some readers decide to wait for a stable
release. That is better than overselling the project and losing trust.

## Positioning

Primary one-line pitch:

> cc-clip helps you paste screenshots into remote AI coding sessions over SSH.

Backup variants:

- Paste images over SSH for Claude Code, Codex CLI, and opencode.
- A small SSH clipboard bridge for remote AI coding workflows.
- Copy a screenshot locally, paste it into the remote agent session.

Use this framing:

- Problem first: remote AI coding loses local image paste and notifications.
- Mechanism second: local daemon, SSH tunnel, remote shim or X11 bridge.
- Outcome third: less manual upload friction when working over SSH.
- Boundary always: beta, SSH-specific, macOS primary path, Windows experimental.

Avoid this framing:

- Do not call it universal clipboard sync.
- Do not imply partnership with Anthropic, OpenAI, opencode, or Google.
- Do not claim Antigravity image paste support yet.
- Do not say "works with any SSH host" without prerequisites.
- Do not ask for stars before providing value.

## Install Snippet For Beta

Because `v0.9.0-beta.1` is a prerelease, use the pinned installer form:

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=v0.9.0-beta.1 sh
cc-clip setup myserver
```

For hosts that use Claude Code and Codex CLI together:

```sh
cc-clip setup myserver --all
```

For opencode-only hosts:

```sh
cc-clip setup myserver --opencode
```

For Antigravity today:

```sh
cc-clip setup myserver --agy
```

Antigravity currently means notification setup only. Do not present it as image
paste support.

## Relationship Rules

These rules matter more than reach.

- Disclose that you are the maintainer.
- Share only where the problem is already being discussed.
- Tailor every GitHub issue or community reply to the thread.
- Use one short link after explaining why it may help.
- If a maintainer says the reply is off-topic, thank them and do not argue.
- Do not post the same copy across several issues in the same project.
- When someone reports a failure, treat it as product feedback, not a debate.
- Thank people for edge cases even when they are not ready to try the tool.

Default reply posture:

> I built this because I hit the same workflow problem. It may or may not fit
> your setup, but I would appreciate feedback if you try it.

## Demo Plan

Target length: 15 to 25 seconds.

Storyboard:

1. Show a screenshot copied on the local Mac.
2. Show an SSH session to a Linux host.
3. Open Claude Code, Codex CLI, or opencode.
4. Press paste.
5. Show the image appearing in the remote agent session.
6. End on the repo URL and beta install command.

Suggested captions:

- `Local screenshot`
- `Remote AI coding session over SSH`
- `Paste image without manual SCP`
- `cc-clip v0.9.0-beta.1 beta`

Avoid heavy architecture diagrams in the first demo. Let the video prove the
workflow before the post explains the mechanism.

## GitHub Release Notes

Standalone file: `docs/marketing/v0.9.0-beta.1-release-notes.md`.

Current public state checked on 2026-06-04: the GitHub release page already has a
human-readable opt-in beta summary. The standalone file is an ASCII-safe mirror
of that public body, so future edits can be reviewed locally before any approved
`gh release edit`.

## Show HN

Recommended title:

`Show HN: cc-clip - paste screenshots into remote AI coding sessions over SSH`

Post body:

```text
I built cc-clip because image paste kept breaking in my remote AI coding setup.

The workflow was simple: take a screenshot on my Mac, SSH into a Linux machine,
open Claude Code or Codex CLI, press paste, and then realize the remote session
could not see my local clipboard.

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
Windows is still experimental, and Antigravity support is notify-only today.
I am mainly looking for practical edge cases from people who use remote agents
daily.
```

## X Short Post

```text
I released cc-clip v0.9.0-beta.1.

It is meant to help paste screenshots into remote Claude Code, Codex CLI, and
opencode sessions over SSH, without patching those tools.

This is still a beta. Feedback from remote AI coding users would be very useful:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## X Thread

```text
1/ I built cc-clip because my local screenshots kept getting stuck at the SSH
boundary.

2/ The common workflow: copy an image locally, SSH into a Linux host, open an AI
coding agent, press paste, and the remote clipboard is empty.

3/ cc-clip bridges that gap with a local clipboard daemon, SSH RemoteForward,
and remote clipboard adapters.

4/ Claude Code and opencode use the xclip / wl-paste shim path. Codex CLI uses
Xvfb plus a small X11 bridge because it reads the clipboard differently.

5/ v0.9.0-beta.1 adds target-aware setup: `--codex`, `--opencode`, `--agy`, and
`--all`. Breaking change: `--codex` now means Codex-only; use `--all` for
Claude Code plus Codex.

6/ This is a beta. macOS -> Linux is the primary path, Windows is experimental,
and Antigravity is notify-only today.

7/ If this matches your remote AI coding workflow, feedback would help:
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## Reddit

Recommended first targets:

- `r/ClaudeAI`, only if self-promotion rules allow it
- `r/commandline`, if the post is framed around the SSH/CLI implementation
- `r/LLMDevs`, if the community accepts open-source workflow tools

Post body:

````markdown
I maintain an open-source tool called [cc-clip](https://github.com/ShunmeiCho/cc-clip).
I built it for a specific remote AI coding problem: image paste breaks when the
agent runs on a Linux server over SSH but the screenshot is on your local machine.

The current beta, `v0.9.0-beta.1`, has target-aware setup paths for Claude Code,
Codex CLI, and opencode. It also wires notifications for supported agents. The
primary tested path is macOS -> remote Linux; Windows is available through a
separate experimental hotkey/SCP workflow.

Roughly how it works:

- local clipboard daemon
- SSH RemoteForward
- xclip / wl-paste shim for Claude Code and opencode
- Xvfb + X11 bridge for Codex CLI

Beta install:

```sh
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=v0.9.0-beta.1 sh
cc-clip setup myserver --all
```

I am sharing it here because I would like feedback from people who actually use
remote coding agents over SSH. SSH config edge cases, Codex headless setups, and
opencode clipboard behavior are especially useful to hear about.
````

## GitHub Issue Reply Template

Use only when the thread is already about remote clipboard, image paste,
notifications over SSH, X11 clipboard behavior, or a directly related setup
problem.

```text
I maintain a small open-source workaround for a related remote-SSH clipboard
problem, so I wanted to share it in case it helps anyone here:

https://github.com/ShunmeiCho/cc-clip

It is not an official integration, and it may not fit every setup. The current
beta is focused on macOS -> remote Linux workflows, with separate paths for
Claude Code/opencode (`xclip`/`wl-paste`) and Codex CLI (Xvfb + X11 bridge).
Antigravity support is notification-only today.

If this is too far from the issue topic, I am happy to remove the comment.
If anyone tries it, I would appreciate hearing where it works or breaks.
```

## Reply Playbook

When someone says "Why not X11 forwarding?":

```text
That is a fair option, especially if you already use X11 forwarding. I wanted a
narrower setup for image paste and agent notifications, without forwarding a
full graphical session. cc-clip is probably not worth it if X11 forwarding
already works well for your workflow.
```

When someone says "This is too much setup":

```text
I agree it is not for every workflow. The target user is someone who spends a lot
of time in remote AI coding sessions and pastes screenshots often enough that
manual upload becomes friction. If image paste is rare, plain `scp` is simpler.
```

When someone reports a failure:

```text
Thanks for trying it and for the details. This is exactly the kind of beta
feedback I am looking for. Could you share local OS, remote OS, target CLI, the
setup command you ran, and `cc-clip doctor --host <host>` output with any
sensitive paths or hostnames redacted?
```

When someone challenges support claims:

```text
Good point. I should be more precise: macOS -> remote Linux is the primary
stable path, Windows is experimental, and Antigravity is notification-only today.
I will tighten that wording where it is unclear.
```

## Seven-Day Sequence

### Day 0: Prep

- Confirm the GitHub release notes are human-readable, not just auto changelog.
- Confirm the README quick start mentions the beta install pin.
- Refresh the demo if it still shows an old version string.
- Pick one canonical URL for the beta release.

### Day 1: Main Launch

- Post the short X announcement.
- Post Show HN only if you can stay available for the first few hours.
- Reply calmly to the first questions and collect setup edge cases.

### Day 2: Targeted Community Sharing

- Share one tailored Reddit post if subreddit rules allow it.
- Reply to at most 2 relevant GitHub issues, only where cc-clip is directly
  relevant.

### Day 3: Follow-Up

- Post the X thread if the first post led to useful questions.
- Turn repeated questions into docs issues or README edits.

### Day 4: Feedback Triage

- Group feedback into setup friction, compatibility gaps, docs confusion, and
  feature requests.
- Do not argue with negative feedback. Extract the signal and move on.

### Day 5: Proof Points

- Share only concrete proof points: confirmed environments, bugs fixed, docs
  improved, or useful community suggestions.
- Avoid vanity metrics unless they are relevant to the follow-up.

### Day 6: Second Wave

- Follow up in threads where people asked for updates.
- Do not repost in communities that did not engage.

### Day 7: Review

- Decide whether to continue with community feedback or wait until the stable
  v0.9.0 release.
- Update this launch kit with the actual objections and responses that worked.
