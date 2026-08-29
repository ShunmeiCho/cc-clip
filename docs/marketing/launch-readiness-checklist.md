# cc-clip Launch Readiness Checklist

Use this as the final go/no-go checklist before any public promotion.

Directory index: `docs/marketing/README.md`.

Status checked locally on 2026-06-04. Public actions still require explicit
approval.

## Current State

| Area | Status | Evidence |
|---|---|---|
| GitHub release | Ready | `v0.9.0-beta.1` is published as a prerelease |
| Release notes | Ready | Public page has a human-readable opt-in beta summary |
| Demo GIFs | Ready | `demo-quick.gif` and `demo-macos.gif` regenerated from beta-pinned tapes |
| Public copy | Ready | `global-launch-kit.md`, `social-posts.md`, and `x-article-launch.md` |
| Chinese copy | Ready | `chinese-launch-kit.md` |
| Approval flow | Ready | `promotion-approval-runbook.md` |
| GitHub issue targets | Ready | `community-outreach-targets.md` |
| Reply handling | Ready | `post-launch-response-playbook.md` |
| Tone gate | Ready | Public copy is framed as beta feedback, not broad promotion |
| External posting | Done for first action | `openai/codex#25465` reply posted and verified |

## Required Boundaries

Every public message must preserve these facts:

- This is `v0.9.0-beta.1`, an opt-in prerelease.
- macOS -> remote Linux is the primary tested path.
- Windows support is experimental and uses `send` / `hotkey`.
- Codex CLI uses Xvfb + x11-bridge and may need remote `sudo` or manual Xvfb.
- Antigravity is notification-only today; image paste is not implemented.
- cc-clip is an independent open-source tool, not an official integration.

## Do Not Launch If

- You cannot monitor replies for the first few hours.
- The release page or README has drifted from the support boundary above.
- A known setup bug affects the target audience you plan to reach.
- You are tempted to post the same copy across several communities.
- You do not have approval for the exact public action.

## Tone Self-Check

Before using any public draft, confirm:

- The post answers a problem already present in that channel or thread.
- The copy discloses maintainership when linking cc-clip in someone else's
  space.
- The beta boundary appears before or near any install command.
- The reader can decide "not for my workflow" without feeling pushed.
- The copy avoids broad words like "fix", "works everywhere", "one command",
  "best", or "official".
- A maintainer, moderator, or community member can ask you to stop without
  creating an argument.

## First Action Recommendation

Choose one path:

### Technical-prior-art path

Completed first action:

- `openai/codex#25465`
- `https://github.com/openai/codex/issues/25465#issuecomment-4618335016`

Next step:

- Monitor direct replies.
- Do not post another GitHub issue reply the same day.

### User-help path

Do not use `anthropics/claude-code#5277` as a first action anymore.
`ShunmeiCho` already replied there with cc-clip on 2026-03-06, so another
project link would be repetitive unless someone asks for a direct follow-up.

### Broadcast path

Best first action:

```text
允许发布 X short launch post。
```

Why:

- Low ceremony.
- Easy to link to the beta release.
- Does not intrude into upstream issue trackers.

Risk:

- Lower feedback quality than a targeted GitHub issue reply.

## Approval Checklist

Before executing any public action, confirm:

- Exact channel or issue URL.
- Exact draft to use.
- Whether replies will be monitored today.
- Whether to stop after one action or continue to a second action.

## Post-Action Checklist

After any public action:

- Save the URL of the post or comment.
- Watch replies for setup failures or support-boundary corrections.
- Record public actions and useful feedback in `feedback-log.md`.
- Use `post-launch-response-playbook.md` for replies.
- If two people report the same issue, stop promotion and open a docs/code
  follow-up.
- If a maintainer says the comment is off-topic, apologize and stop.

## One-Day Launch Cap

To preserve trust, do not exceed this in one day:

- 1 X post or thread
- 1 Show HN post
- 1 Reddit/community post
- 1 GitHub issue reply

Prefer fewer actions with careful follow-up over broader distribution.

## Ready-To-Use Entrypoints

- Public copy: `docs/marketing/social-posts.md`
- Chinese copy: `docs/marketing/chinese-launch-kit.md`
- Long article: `docs/marketing/x-article-launch.md`
- GitHub issue targets: `docs/marketing/community-outreach-targets.md`
- Approval commands: `docs/marketing/promotion-approval-runbook.md`
- Reply handling: `docs/marketing/post-launch-response-playbook.md`
- Release notes mirror: `docs/marketing/v0.9.0-beta.1-release-notes.md`
