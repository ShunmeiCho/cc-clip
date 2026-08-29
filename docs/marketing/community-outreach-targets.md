# cc-clip Community Outreach Targets

This file is a current-state shortlist for relationship-preserving outreach.
It is not a posting queue. Every public reply still needs explicit approval for
the exact issue URL.

Checked on 2026-06-04 with read-only `gh issue view` / `gh issue list`.
Refreshed on 2026-06-04 after confirming existing `ShunmeiCho` replies in
`anthropics/claude-code#5277`.

## Rules For This List

- Reply only when cc-clip directly helps the thread's actual problem.
- Prefer one useful, tailored technical note over a generic project link.
- Do not repeat a cc-clip comment in threads where `ShunmeiCho` already replied.
- Do not reply to broad local-terminal clipboard issues unless the thread
  explicitly includes remote SSH or headless Linux.
- If another maintainer or user says the link is off-topic, thank them and stop.

## Current GitHub Reply Status

Prepared body file: `docs/marketing/openai-codex-25465-comment.md`.

### Codex #25465 - external clipboard reader for headless environments

URL: `https://github.com/openai/codex/issues/25465`

Current state:

- Open.
- Latest visible activity on 2026-05-31 discusses why `#24908` and this issue
  are complementary rather than duplicates.
- `ShunmeiCho` replied on 2026-06-04:
  `https://github.com/openai/codex/issues/25465#issuecomment-4618335016`
- The issue proposes an env-gated external clipboard reader for headless
  environments and explicitly names macOS host + Linux container, Windows
  outside WSL, headless Linux, and SSH sessions with no display forwarding.

Fit:

- Strong technical fit as prior art, not as a generic product link.
- The reply should emphasize implementation experience: Xvfb + X11 bridge,
  SSH tunnel, and the limitation that this is external tooling rather than a
  Codex patch.

Follow-up rule:

- Do not post another project link in this issue.
- Use `docs/marketing/openai-codex-25465-followups.md` only for direct
  questions or corrections.

## Watch, But Do Not Reply Yet

### Claude Code #64072 - paste image from clipboard directly in terminal

URL: `https://github.com/anthropics/claude-code/issues/64072`

Reason to wait:

- The issue is open but labeled `duplicate`.
- It is broad local terminal UX, not specifically remote SSH.
- A cc-clip link could look like opportunistic promotion unless a remote SSH
  user joins the thread.

Use only if:

- The thread remains open and a user explicitly asks for a remote SSH workaround.

### Codex #23611 - WSL image paste fallback

URL: `https://github.com/openai/codex/issues/23611`

Reason to wait:

- The issue is specifically WSL fallback behavior when `appendWindowsPath=false`.
- cc-clip's Windows path is experimental and separate from the Codex Xvfb path.
- A reply could confuse the support boundary.

Use only if:

- A maintainer or user asks for external-reader examples across host/guest
  boundaries.

## Do Not Reply Again

### Codex #25465 - external clipboard reader for headless environments

URL: `https://github.com/openai/codex/issues/25465`

Reason:

- `ShunmeiCho` replied with a prior-art note on 2026-06-04.
- Do not add another cc-clip comment unless a maintainer or issue participant
  asks a direct question.

### Claude Code #5277 - image paste in SSH

URL: `https://github.com/anthropics/claude-code/issues/5277`

Reason:

- `ShunmeiCho` already replied with cc-clip twice on 2026-03-06.
- The issue remains a strong fit for cc-clip's core Mac -> SSH -> remote Linux
  path, but a third project link would look repetitive.
- If someone directly asks `ShunmeiCho` for current beta status, answer only the
  question and avoid reposting the full launch copy.

### Codex #13716 - clipboard image paste failure on Arch Linux

URL: `https://github.com/openai/codex/issues/13716`

Reason:

- `ShunmeiCho` already replied with cc-clip on 2026-03-12.
- Do not repeat the project link. If revisiting later, only post a short update
  when someone asks about current beta status.

### opencode #19294 - image paste over SSH invalid image data

URL: `https://github.com/anomalyco/opencode/issues/19294`

Reason:

- `ShunmeiCho` already replied with cc-clip on 2026-04-21.
- Do not add another promotional comment.

### Claude Code #19976 - terminal notifications in tmux

URL: `https://github.com/anthropics/claude-code/issues/19976`

Reason:

- `ShunmeiCho` already replied with cc-clip notification workaround on
  2026-04-01.
- Recent replies are now discussing other notification approaches. Do not
  re-enter unless someone asks about cc-clip specifically.

## Poor Fits For cc-clip Promotion

### opencode #15907 / #12800 / #4283

Reason:

- These are mostly clipboard copy, OSC52, local macOS, or terminal behavior
  threads.
- Several already have concrete xclip / wl-clipboard / tmux passthrough
  workarounds.
- cc-clip can help some remote image-paste paths, but a link in these broad copy
  threads would likely feel off-topic.

### Codex #19143 / #24322

Reason:

- These focus on local macOS direct paste / Cmd-V behavior and terminal shortcut
  handling.
- cc-clip is aimed at remote SSH and headless Linux paths, so it should not be
  presented as a fix for these.

## Recommended Next Public Step

The first GitHub issue reply has been posted:

1. `openai/codex#25465`
2. Comment URL:
   `https://github.com/openai/codex/issues/25465#issuecomment-4618335016`

Next step: monitor for direct replies. Do not post another GitHub issue reply
the same day unless there is a clear user request and enough time to respond.
