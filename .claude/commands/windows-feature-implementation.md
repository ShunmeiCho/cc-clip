---
name: windows-feature-implementation
description: Workflow command scaffold for windows-feature-implementation in cc-clip.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /windows-feature-implementation

Use this workflow when working on **windows-feature-implementation** in `cc-clip`.

## Goal

Implements or extends Windows-specific features, including hotkey, tray, autostart, and clipboard support.

## Common Files

- `cmd/cc-clip/hotkey_windows.go`
- `cmd/cc-clip/hotkey_windows_test.go`
- `cmd/cc-clip/hotkey_config_windows.go`
- `cmd/cc-clip/hotkey_config_windows_test.go`
- `cmd/cc-clip/tray_windows.go`
- `cmd/cc-clip/send_windows.go`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Modify or add files under cmd/cc-clip/ with _windows.go, _windows_test.go, tray_windows.go, send_windows.go, hotkey_windows.go, hotkey_config_windows.go
- Update or add files under internal/service/ with autostart_windows.go or schtasks_windows.go
- Update or add files under internal/daemon/clipboard_windows.go
- Update Makefile and .goreleaser.yaml for Windows targets if needed
- Update README.md and/or docs/windows-quickstart.md for documentation

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.