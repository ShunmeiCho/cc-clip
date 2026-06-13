---
name: feature-development-with-tests
description: Workflow command scaffold for feature-development-with-tests in cc-clip.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /feature-development-with-tests

Use this workflow when working on **feature-development-with-tests** in `cc-clip`.

## Goal

Implements a new feature or refactors a subsystem, updating implementation and corresponding test files together.

## Common Files

- `internal/daemon/clipboard_windows.go`
- `internal/daemon/clipboard_windows_test.go`
- `internal/daemon/server.go`
- `internal/daemon/server_test.go`
- `internal/shim/template.go`
- `internal/shim/template_test.go`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Edit or add implementation files in internal/*/*.go
- Edit or add corresponding test files in internal/*/*_test.go

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.