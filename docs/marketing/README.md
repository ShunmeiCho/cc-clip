# cc-clip Marketing Materials

This directory contains the local launch package for `cc-clip v0.9.0-beta.1`.

Public posting is not automatic. Every external action still needs explicit
maintainer approval for the exact channel, issue, or post.

## Start Here

1. Read `launch-readiness-checklist.md`.
2. Pick one approved public action.
3. Use `promotion-approval-runbook.md` for the exact approval wording and command.
4. Use `post-launch-response-playbook.md` for replies after posting.

## Current Boundary

All public copy must preserve this support boundary:

- `v0.9.0-beta.1` is an opt-in prerelease.
- macOS -> remote Linux is the primary tested path.
- Windows support is experimental and uses `send` / `hotkey`.
- Codex CLI uses Xvfb + x11-bridge and may need remote `sudo` or manual Xvfb.
- Antigravity is notification-only today; image paste is not implemented.
- cc-clip is independent open source, not an official integration.

## Launch Control

| File | Use |
|---|---|
| `launch-readiness-checklist.md` | Final go/no-go checklist before public action |
| `promotion-approval-runbook.md` | Approval phrases, exact public-action commands, stop conditions |
| `promotion-preflight-audit.md` | Current audit of ready materials and remaining public-action gate |
| `community-outreach-targets.md` | Current GitHub issue shortlist and per-issue draft replies |
| `post-launch-response-playbook.md` | Reply templates, feedback triage, relationship-risk handling |
| `feedback-log.md` | Post-action feedback and stop-condition record |
| `openai-codex-25465-approval-packet.md` | One-page approval packet for the prepared Codex issue reply |
| `openai-codex-25465-followups.md` | Specific follow-up handling for the prepared Codex issue reply |

## Copy Assets

| File | Use |
|---|---|
| `global-launch-kit.md` | Source of truth for the first English beta launch wave |
| `social-posts.md` | X, Hacker News, Reddit, GitHub discussion, and issue-reply copy |
| `chinese-launch-kit.md` | Chinese-language posts, private asks, and community replies |
| `x-article-launch.md` | Longer maintainer-style article draft |
| `v0.9.0-beta.1-release-notes.md` | ASCII-safe local mirror of the public beta release notes |
| `openai-codex-25465-comment.md` | Pure body file for the prepared Codex issue reply |

## Demo Assets

| File | Use |
|---|---|
| `demo-quick.gif` | Short beta demo for README and launch posts |
| `demo-macos.gif` | Longer macOS setup demo |
| `demo-windows.gif` | Existing Windows demo; do not use as the lead beta asset |
| `demo-quick.tape` / `demo-macos.tape` | VHS source for regenerated beta-pinned GIFs |
| `sim-quick.sh` / `sim-macos.sh` | Simulated command output for VHS demos |

## Current Public Status

The first GitHub issue reply has been posted and verified:

- `https://github.com/openai/codex/issues/25465#issuecomment-4618335016`

Next step: monitor direct replies and use `openai-codex-25465-followups.md` if
someone asks a concrete question.

Any additional public action still needs separate explicit approval for the
exact channel, issue, or post. Do not do several public actions in one burst.
The launch package is designed for small, monitored moves with time to respond.

Do not reply again in `anthropics/claude-code#5277` unless someone directly asks
for a cc-clip follow-up; `ShunmeiCho` has already posted there.

## Daily Rule

If feedback reveals a support-boundary mistake, setup bug, or community concern,
pause promotion and update docs or code first.
