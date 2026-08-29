# cc-clip Beta Promotion Design

## Purpose

Promote `cc-clip v0.9.0-beta.1` to developers who already use remote AI coding
tools over SSH.

The promotion should be humble and relationship-preserving. The project is
useful, but it is still a beta. The right launch outcome is thoughtful feedback
and trust, not aggressive reach.

## Current Product Truth

Facts to preserve in every launch asset:

- `v0.9.0-beta.1` is a GitHub prerelease.
- The release workflow completed successfully and published archives plus
  `checksums.txt`.
- The primary stable path is macOS -> remote Linux.
- Windows support exists through `send` / `hotkey`, but remains experimental.
- Claude Code and opencode use the `xclip` / `wl-paste` shim path.
- Codex CLI uses Xvfb + x11-bridge and can require remote `sudo` or manual Xvfb
  installation.
- Antigravity support is notification-only today; clipboard paste is pending.
- Because this is a prerelease, beta installs need the pinned installer form:

  ```sh
  curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | CC_CLIP_VERSION=v0.9.0-beta.1 sh
  ```

## Audience

Primary audience:

- Developers using Claude Code, Codex CLI, or opencode on remote Linux hosts.
- macOS users who rely on screenshots while debugging remote agent sessions.
- Users with SSH, tmux, headless Linux, VS Code Remote SSH, or jump-host
  workflows.

Secondary audience:

- CLI and terminal developers.
- Open-source maintainers who care about remote development workflows.
- Developers evaluating notification bridges for remote agents.

Do not aim at a broad "AI productivity" audience yet. The project is easier to
trust when it is framed around a specific workflow.

## Positioning

Core message:

> cc-clip helps you paste screenshots into remote AI coding sessions over SSH.

Supporting messages:

- It solves a concrete remote workflow pain: local image clipboard does not
  naturally cross the SSH boundary.
- It uses the existing clipboard paths of the tools instead of patching them.
- It now has target-aware setup for Claude Code, Codex CLI, opencode, and agy
  notifications.
- It is open source and wants feedback on real remote setups.

## Voice

Use this tone:

- specific
- technically honest
- friendly but not needy
- appreciative of maintainers and community norms
- comfortable saying "this may not fit your setup"

Avoid this tone:

- "finally solved"
- "works everywhere"
- "drop-in replacement"
- "zero setup"
- "all platforms"
- "official integration"

## Channel Strategy

### 1. GitHub Release Page

The release page is the first public artifact many people will see. It currently
has a human-readable opt-in beta summary, so avoid overwriting it unless a
specific wording change is reviewed and approved.

Action:

- Keep the local ASCII-safe release-notes mirror aligned with the public page.
- Any public release-note edit still needs explicit approval.
- Preserve beta install command, breaking `--codex` change, limits, and feedback
  request in any future edit.

### 2. X

Use X for a concise announcement and optional technical thread.

Purpose:

- Give existing followers and adjacent developers a lightweight way to inspect
  the beta.
- Link to the release, not only the repo, so the beta context is visible.

### 3. Hacker News

Use Show HN only when the maintainer can stay available for early replies.

Purpose:

- Get high-signal technical feedback.
- Surface SSH, X11, and terminal edge cases.

### 4. Reddit

Post only where rules allow open-source project sharing.

Preferred targets:

- `r/ClaudeAI` for workflow pain
- `r/commandline` for the CLI implementation
- `r/LLMDevs` for remote agent feedback

Skip subreddits where the post would feel like drive-by self-promotion.

### 5. GitHub Issues

Use sparingly. This channel has the highest relationship risk.

Rules:

- Reply only to directly relevant issues.
- Disclose maintainership.
- Say the project is not official.
- Offer to remove the comment if it is off-topic.
- Do not post similar comments across many issues in one repository.

## Success Criteria

The launch is successful if it produces:

- specific bug reports or compatibility notes
- confirmed environments where beta works
- docs questions that can be fixed
- respectful discussion with maintainers and users
- some organic stars or watches

Do not treat low install count as failure during beta. A smaller group of
careful early users is better than many confused installs.

## Risks

Self-promotion backlash:

- Mitigation: share only where relevant, tailor copy, and lead with the problem.

Overclaiming support:

- Mitigation: repeat the macOS primary path, Windows experimental status, and
  Antigravity notify-only boundary.

Beta install confusion:

- Mitigation: use the pinned `CC_CLIP_VERSION` install command everywhere.

Support burden:

- Mitigation: ask for structured environment reports and turn repeated issues
  into docs updates.

Relationship risk with upstream tool communities:

- Mitigation: do not imply affiliation, do not pressure maintainers, and remove
  comments if asked.

## Decision

Proceed with a beta launch package, but do not perform external posting or edit
the public GitHub release page without explicit approval.

Recommended next step:

1. Finish local marketing docs.
2. Verify all facts against README and GitHub release state.
3. Ask for approval before updating release notes or posting externally.
