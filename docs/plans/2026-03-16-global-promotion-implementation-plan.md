# cc-clip Beta Promotion Implementation Plan

## Goal

Prepare a ready-to-use `v0.9.0-beta.1` promotion package that is accurate,
humble, and safe to share after maintainer approval.

## Constraints

- Do not post externally without explicit approval.
- Do not edit the GitHub release page without explicit approval.
- Preserve beta boundaries in every public-facing asset.
- Prefer fewer, better-targeted posts over broad repetition.
- Use the pinned prerelease installer command.

## Artifacts

Primary files:

- `docs/marketing/README.md`
- `docs/marketing/global-launch-kit.md`
- `docs/marketing/social-posts.md`
- `docs/marketing/chinese-launch-kit.md`
- `docs/marketing/x-article-launch.md`
- `docs/marketing/v0.9.0-beta.1-release-notes.md`
- `docs/marketing/launch-readiness-checklist.md`
- `docs/marketing/promotion-approval-runbook.md`
- `docs/marketing/community-outreach-targets.md`
- `docs/marketing/post-launch-response-playbook.md`

Planning files:

- `docs/plans/2026-03-16-global-promotion-design.md`
- `docs/plans/2026-03-16-global-promotion-implementation-plan.md`

Demo source files:

- `docs/marketing/sim-quick.sh`
- `docs/marketing/sim-macos.sh`
- `docs/marketing/demo-quick.tape`
- `docs/marketing/demo-macos.tape`

## Task 1: Verify Launch Facts

Evidence to check:

- `git show -s v0.9.0-beta.1`
- `gh release view v0.9.0-beta.1`
- release workflow result for the tag commit
- README platform support and target matrix
- `scripts/install.sh` prerelease pin behavior

Expected outcome:

- The launch kit can cite the beta release as published.
- The wording does not imply a stable release.
- The beta install command uses `CC_CLIP_VERSION=v0.9.0-beta.1`.

## Task 2: Rewrite The Canonical Launch Kit

Create `docs/marketing/README.md` as the directory index so maintainers can find
the readiness checklist, copy assets, target lists, and response playbooks from
one place.

Update `docs/marketing/global-launch-kit.md` with:

- verified facts
- launch goals
- positioning
- install snippets
- relationship rules
- demo plan
- GitHub release notes mirror
- Show HN copy
- X copy
- Reddit copy
- GitHub issue reply template
- response playbook
- seven-day launch sequence

Also create `docs/marketing/v0.9.0-beta.1-release-notes.md` so the public
GitHub release can be updated with one approved command.

Create `docs/marketing/launch-readiness-checklist.md` so maintainers can verify
current state, support boundaries, and next action before any public posting.

Create `docs/marketing/promotion-approval-runbook.md` so every public action has
an explicit approval phrase, command, and relationship-preserving rule.

Create `docs/marketing/community-outreach-targets.md` so GitHub issue outreach
uses verified, per-thread context instead of broad link dropping.

Create `docs/marketing/post-launch-response-playbook.md` so replies after public
posts are calm, bounded, and convert feedback into docs or code work.

Acceptance:

- It can be used as the source of truth for the first beta promotion wave.
- It includes limits for Windows, Codex Xvfb, and Antigravity.
- It contains no "official integration" or "works everywhere" claims.

## Task 3: Rewrite Channel Posts

Update `docs/marketing/social-posts.md` with:

- short X post
- personal X post
- Chinese post
- technical X thread
- Show HN title/body/comment
- Reddit variants
- GitHub issue replies
- GitHub discussion announcement
- direct reply templates

Acceptance:

- Each post is tailored to its channel.
- GitHub issue replies disclose maintainership and offer to remove if off-topic.
- Reddit drafts mention community rules and avoid drive-by self-promotion.

## Task 4: Rewrite The Long Article

Update `docs/marketing/x-article-launch.md` as a maintainer story:

- why the problem exists
- what cc-clip does
- why the implementation is narrow
- what changed in `v0.9.0-beta.1`
- how to try the beta
- boundaries
- feedback request

Acceptance:

- The article is clear without sensational claims.
- It does not use stale source-line counts or old version references.
- It does not claim Antigravity image paste support.

## Task 4.5: Add Chinese-Language Promotion Kit

Create `docs/marketing/chinese-launch-kit.md` with:

- Chinese one-line positioning
- Chinese short post
- WeChat / private ask drafts
- Chinese technical community draft
- Chinese reply templates
- Chinese-specific stop conditions

Acceptance:

- The tone is modest and relational, not salesy.
- It preserves beta, Windows, Codex, and Antigravity boundaries.
- It does not recommend posting to a community before checking that community's
  rules.

## Task 5: Refresh Demo Source Text

Update demo simulation scripts so regenerated GIFs do not show old version text.

Acceptance:

- No demo source prints old release version strings.
- Captions mention the beta or use version-neutral text.
- If GIF regeneration cannot run locally, note that the generated GIF files may
  still need refresh before external posting.

## Task 6: Verification

Run targeted checks:

```sh
rg -n "v0\\.[46]\\.|works everywhere|official integration|all platforms|Antigravity.*image|CC_CLIP_VERSION=v0\\.9\\.0-beta\\.1" docs/marketing docs/plans/2026-03-16-global-promotion-*.md
rg -n "v0\\.[46]\\." docs/marketing/sim-quick.sh docs/marketing/sim-macos.sh docs/marketing/*.tape
git diff -- docs/marketing docs/plans/2026-03-16-global-promotion-*.md
```

Expected outcome:

- The pinned beta install command appears in public drafts.
- Stale version claims are gone.
- Remaining Antigravity mentions are notification-only or clipboard-pending.
- Any external action remains unperformed pending approval.

## Task 7: Approval Gate

After verification, ask for explicit approval before either action:

```sh
gh release edit v0.9.0-beta.1 --notes-file docs/marketing/v0.9.0-beta.1-release-notes.md
```

or posting to X, HN, Reddit, GitHub issues, or Discussions.
