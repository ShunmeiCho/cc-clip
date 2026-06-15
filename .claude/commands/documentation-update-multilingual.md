---
name: documentation-update-multilingual
description: Workflow command scaffold for documentation-update-multilingual in cc-clip.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /documentation-update-multilingual

Use this workflow when working on **documentation-update-multilingual** in `cc-clip`.

## Goal

Updates documentation in multiple languages and related docs to reflect new features, changes, or clarifications.

## Common Files

- `README.md`
- `README.zh-CN.md`
- `README.ja.md`
- `SECURITY.md`
- `docs/*.md`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Edit or add English documentation files (README.md, docs/*.md, SECURITY.md).
- Edit or add translated documentation files (README.zh-CN.md, README.ja.md, etc).
- Synchronize content and clarify differences across languages.

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.