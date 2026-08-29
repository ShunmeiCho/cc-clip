# cc-clip Promotion Approval Runbook

Use this runbook when moving from local launch materials to public promotion.

Public actions are intentionally split so each one can be approved separately.
Do not batch them together unless the maintainer explicitly says to proceed with
the whole launch wave.

Before using this runbook, confirm go/no-go status in
`docs/marketing/launch-readiness-checklist.md`.

For GitHub issue target selection and per-issue draft replies, use
`docs/marketing/community-outreach-targets.md`.

For reply handling after any public post, use
`docs/marketing/post-launch-response-playbook.md`.

The prepared `openai/codex#25465` issue reply has been posted. Use
`docs/marketing/openai-codex-25465-approval-packet.md` as its record and
`docs/marketing/openai-codex-25465-followups.md` for direct follow-ups.

## Current Public State

- `v0.9.0-beta.1` is already published as a GitHub prerelease.
- The release workflow completed successfully for the tag commit.
- The public release body already has a human-readable opt-in beta summary.
- Local launch materials are ready under `docs/marketing/`.
- Demo GIFs have been regenerated from beta-pinned tape scripts.
- `openai/codex#25465` has one verified cc-clip prior-art reply:
  `https://github.com/openai/codex/issues/25465#issuecomment-4618335016`

## Approval Items

### A. Update GitHub Release Notes (Optional)

Why:

- The current public release notes are already human-readable.
- Only update them if the maintainer approves a wording change after review.
- Do not overwrite the release page just because this local file exists.

Command:

```sh
gh release edit v0.9.0-beta.1 --notes-file docs/marketing/v0.9.0-beta.1-release-notes.md
```

Approval wording to ask for:

```text
允许更新 GitHub Release v0.9.0-beta.1 的公开 release notes。
```

Verification after approval:

```sh
gh release view v0.9.0-beta.1 --json body,isPrerelease,isDraft,url
```

### B. Post Short X Announcement

Use:

- `docs/marketing/social-posts.md` -> `Short Launch Post`

Relationship rule:

- Post once.
- Do not immediately ask for stars.
- Reply to setup questions with concrete caveats and docs links.

Approval wording to ask for:

```text
允许发布 X short launch post。
```

### C. Post X Technical Thread

Use:

- `docs/marketing/social-posts.md` -> `Technical Thread`

When:

- After the short announcement receives useful questions, or if the maintainer
  wants a more technical first post.

Relationship rule:

- Keep the thread factual.
- Do not tag upstream projects unless directly relevant and welcome.

Approval wording to ask for:

```text
允许发布 X technical thread。
```

### D. Post Show HN

Use:

- `docs/marketing/social-posts.md` -> `Show HN`
- First comment from the same section.

When:

- Only when the maintainer can monitor replies for the first few hours.

Relationship rule:

- Answer with implementation details when asked.
- Accept criticism without arguing.
- If users call out overclaiming, thank them and tighten wording.

Approval wording to ask for:

```text
允许发布 Show HN，并在前几小时跟进回复。
```

### E. Reddit Community Post

Use:

- `docs/marketing/social-posts.md` -> the subreddit-specific draft.

Before posting:

- Check the subreddit rules.
- Skip the subreddit if project sharing is not clearly allowed.

Relationship rule:

- Disclose maintainership.
- Lead with the problem and beta limits.
- Do not cross-post identical text to many subreddits on the same day.

Approval wording to ask for:

```text
允许发布到 <subreddit>，并使用对应草稿。
```

### F. GitHub Issue Replies

Use for future issue replies only after a new exact issue URL is approved:

- `docs/marketing/social-posts.md` -> `GitHub Issue Replies`
- `docs/marketing/community-outreach-targets.md` for do-not-repeat checks

Before replying:

- Confirm the issue is directly about remote clipboard, image paste,
  notifications over SSH, X11 clipboard behavior, or opencode clipboard behavior.
- Read the latest issue comments before replying.

Relationship rule:

- One tailored reply per issue.
- Disclose maintainership.
- Say it is not an official integration.
- Offer to remove the comment if off-topic.
- Do not reply to many issues in one repository in a short burst.

Approval wording to ask for:

```text
允许回复这个具体 GitHub issue: <url>
```

Do not post another reply to `openai/codex#25465` unless a maintainer or issue
participant asks a direct question.

## Recommended Order

1. Review the current GitHub release notes; update only if a wording change is
   explicitly approved.
2. Post the short X announcement.
3. Wait for early questions.
4. Post Show HN only when there is time to respond.
5. Post one targeted community thread if rules allow it.
6. Reply to GitHub issues only when a specific issue is directly relevant.

## Stop Conditions

Stop public promotion for the day if:

- a setup bug affects several early users
- a support-boundary claim is challenged and needs correction
- a community moderator or maintainer says the post is off-topic
- the maintainer cannot keep up with replies
- repeated questions show the docs or release notes are unclear

In those cases, update docs or release notes first, then resume later.
