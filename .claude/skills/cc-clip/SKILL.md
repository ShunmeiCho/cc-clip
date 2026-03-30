```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview

This skill teaches you how to contribute to the `cc-clip` Go codebase, a cross-platform clipboard utility with a focus on Windows and Codex/X11 bridge support. You'll learn the project's coding conventions, commit patterns, and step-by-step workflows for implementing features, fixing bugs, expanding documentation, and writing tests. This guide is ideal for new contributors or anyone aiming to maintain consistency and quality in the repository.

## Coding Conventions

**File Naming**
- Use `snake_case` for file names.
  - Example: `hotkey_windows.go`, `autostart_windows_test.go`

**Import Style**
- Use **relative imports** within the project.
  - Example:
    ```go
    import (
        "internal/service"
        "cmd/cc-clip"
    )
    ```

**Export Style**
- Use **named exports** for functions, types, and variables.
  - Example:
    ```go
    // Exported function
    func StartTray() error { ... }

    // Exported type
    type ClipboardService struct { ... }
    ```

**Commit Patterns**
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  - Prefixes: `fix`, `docs`, `feat`, `test`
  - Example:
    ```
    feat: add hotkey support for Windows tray menu
    fix: resolve clipboard race condition on Windows
    docs: update windows quickstart guide
    test: add tests for autostart_windows.go
    ```
- Average commit message length: ~56 characters

## Workflows

### Windows Feature Implementation
**Trigger:** When adding or enhancing Windows platform support or features  
**Command:** `/add-windows-feature`

1. Modify or add files under `cmd/cc-clip/` with suffixes like `_windows.go`, `_windows_test.go`, `tray_windows.go`, `send_windows.go`, `hotkey_windows.go`, `hotkey_config_windows.go`.
2. Update or add files under `internal/service/` such as `autostart_windows.go` or `schtasks_windows.go`.
3. Update or add files under `internal/daemon/clipboard_windows.go`.
4. Update `Makefile` and `.goreleaser.yaml` for Windows targets if needed.
5. Update `README.md` and/or `docs/windows-quickstart.md` for documentation.

**Example:**
```go
// cmd/cc-clip/hotkey_windows.go
func RegisterHotkey() error {
    // Windows-specific hotkey logic
}
```

---

### Windows Feature Test and Refactor
**Trigger:** When adding or improving test coverage or refactoring Windows-specific logic  
**Command:** `/test-windows-feature`

1. Modify or add `*_windows_test.go` files in `cmd/cc-clip/` and `internal/service/`.
2. Refactor related `*_windows.go` files for testability or DRY logic.
3. Update or add tests for new or fixed behaviors.

**Example:**
```go
// cmd/cc-clip/hotkey_windows_test.go
func TestRegisterHotkey(t *testing.T) {
    err := RegisterHotkey()
    if err != nil {
        t.Fatal(err)
    }
}
```

---

### Windows Bugfix Iteration
**Trigger:** When addressing PR review feedback or fixing bugs in Windows-specific code  
**Command:** `/fix-windows-bug`

1. Modify multiple `*_windows.go` and `*_windows_test.go` files in `cmd/cc-clip/` and `internal/service/`.
2. Update related logic in `send.go`, `tray_windows.go`, or `clipboard_windows.go`.
3. Add or update tests to cover new fixes.
4. Sometimes update `Makefile` or documentation.

---

### Documentation Expansion and Update
**Trigger:** When documenting new features, updating guides, or clarifying troubleshooting  
**Command:** `/update-docs`

1. Edit `README.md` to add or update sections, diagrams, or troubleshooting.
2. Add or update `docs/*.md` files for quickstart, troubleshooting, or plans.
3. Optionally update or add images (e.g., `docs/logo.png`, `img/image.png`).

**Example:**
```markdown
## Windows Quickstart

See [docs/windows-quickstart.md](docs/windows-quickstart.md) for setup instructions.
```

---

### Codex Support Feature Workflow
**Trigger:** When adding or enhancing Codex CLI/X11 bridge support  
**Command:** `/add-codex-support`

1. Add or update files under `internal/x11bridge/` and `internal/xvfb/`.
2. Update `cmd/cc-clip/main.go` for CLI integration.
3. Update or add `internal/shim/deploy.go`, `pathfix.go` for deploy state and marker logic.
4. Update `README.md` and `docs/CLAUDE.md` for documentation.

**Example:**
```go
// internal/x11bridge/bridge.go
func StartBridge() error {
    // X11 bridge logic
}
```

## Testing Patterns

- Test files follow the pattern: `*_test.go` (e.g., `hotkey_windows_test.go`).
- Test files are placed alongside their implementation files.
- The testing framework is not explicitly specified, but standard Go testing is implied.
- Example test:
    ```go
    // internal/service/autostart_windows_test.go
    func TestEnableAutostart(t *testing.T) {
        err := EnableAutostart()
        if err != nil {
            t.Errorf("EnableAutostart failed: %v", err)
        }
    }
    ```

## Commands

| Command              | Purpose                                                      |
|----------------------|--------------------------------------------------------------|
| /add-windows-feature | Implement or extend Windows-specific features                |
| /test-windows-feature| Add or improve tests and refactor Windows-specific logic     |
| /fix-windows-bug     | Fix bugs and iterate on Windows platform code                |
| /update-docs         | Expand or update documentation                               |
| /add-codex-support   | Implement or enhance Codex CLI/X11 bridge support            |
```