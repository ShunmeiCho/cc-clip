---
name: feature-development-implementation-tests
description: Workflow command scaffold for feature-development-implementation-tests in cc-clip.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /feature-development-implementation-tests

Use this workflow when working on **feature-development-implementation-tests** in `cc-clip`.

## Goal

Implements a new feature or significant refactor, updating main implementation files and corresponding test files together.

## Common Files

- `internal/daemon/*.go`
- `internal/daemon/*_test.go`
- `internal/tunnel/*.go`
- `internal/tunnel/*_test.go`
- `internal/x11bridge/*.go`
- `internal/x11bridge/*_test.go`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Edit or create implementation files in internal/daemon/, internal/tunnel/, internal/x11bridge/, or similar.
- Edit or create corresponding *_test.go files for new or changed logic.
- Optionally update related files in internal/shim/ or internal/plugin/.

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.