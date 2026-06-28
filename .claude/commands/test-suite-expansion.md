---
name: test-suite-expansion
description: Workflow command scaffold for test-suite-expansion in cc-clip.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /test-suite-expansion

Use this workflow when working on **test-suite-expansion** in `cc-clip`.

## Goal

Adds or updates multiple test files across different modules to improve coverage or portability.

## Common Files

- `internal/*/*_test.go`
- `cmd/cc-clip/main_test.go`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Edit or add *_test.go files across several internal/ directories.
- Update or create test helpers as needed.

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.