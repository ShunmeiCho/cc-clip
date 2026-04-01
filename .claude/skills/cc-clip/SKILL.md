```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview

This skill teaches you the core development patterns, coding conventions, and workflows used in the `cc-clip` Go codebase. You'll learn how to implement new features, fix bugs, extend platform support, and enhance documentation while following the project's established style and structure. The repository uses conventional commits, a modular directory layout, and emphasizes test coverage and clear documentation.

---

## Coding Conventions

**File Naming:**
- Use `snake_case` for file names.
  - Example: `clipboard_utils.go`, `windows_clipboard.go`

**Imports:**
- Use relative import paths within the module.
  - Example:
    ```go
    import (
        "fmt"
        "os"
        "github.com/yourorg/cc-clip/internal/clipboard"
    )
    ```

**Exports:**
- Use named exports for functions, types, and variables that should be accessible outside the package.
  - Example:
    ```go
    // Exported function
    func CopyToClipboard(text string) error {
        // ...
    }

    // Unexported (internal) function
    func parseArgs() []string {
        // ...
    }
    ```

**Commit Messages:**
- Use [Conventional Commits](https://www.conventionalcommits.org/).
- Prefixes: `fix`, `docs`, `feat`
- Example:  
  ```
  feat: add cross-platform clipboard support for images
  fix: handle empty clipboard gracefully
  docs: update windows quickstart guide
  ```

---

## Workflows

### Feature Implementation with Tests and Docs

**Trigger:** When adding a major new capability or subsystem  
**Command:** `/new-feature`

1. Add or modify implementation files in `cmd/cc-clip/`, `internal/`, or similar directories.
2. Add or update corresponding `*_test.go` files to ensure test coverage.
3. Add or update design documentation in `docs/plans/`.
4. Update `README.md` or other user-facing docs if needed.

**Example:**
```go
// cmd/cc-clip/image_clipboard.go
func CopyImageToClipboard(img image.Image) error {
    // implementation
}
```
```go
// cmd/cc-clip/image_clipboard_test.go
func TestCopyImageToClipboard(t *testing.T) {
    // test logic
}
```
---

### Windows Platform Support Extension

**Trigger:** When adding or improving Windows-specific compatibility/features  
**Command:** `/windows-support`

1. Add or modify Windows-specific files (suffix `_windows.go`, `_other.go`) in relevant directories.
2. Add or update `*_windows_test.go` files for Windows code.
3. Update platform-specific documentation (e.g., `docs/windows-quickstart.md`, `README.md`).
4. Update `Makefile` and `.goreleaser.yaml` for Windows build targets.

**Example:**
```go
// internal/clipboard/windows_clipboard_windows.go
func copyToClipboardWindows(data []byte) error {
    // Windows API calls
}
```
---

### Bugfix with Targeted Test Update

**Trigger:** When fixing a bug and ensuring it is covered by tests  
**Command:** `/bugfix`

1. Modify implementation files to fix the bug.
2. Add or update a relevant `*_test.go` file to cover the bug scenario.
3. Reference the bug or PR in the commit message.

**Example:**
```go
// internal/clipboard/clipboard.go
func PasteFromClipboard() (string, error) {
    // fixed logic
}
```
```go
// internal/clipboard/clipboard_test.go
func TestPasteFromClipboard_Empty(t *testing.T) {
    // test for empty clipboard case
}
```
---

### Documentation Enhancement or Troubleshooting Update

**Trigger:** When clarifying, expanding, or visually improving documentation  
**Command:** `/docs-update`

1. Edit or expand `README.md` with new sections, diagrams, or troubleshooting.
2. Add or update files in `docs/` (e.g., plans, troubleshooting, marketing, or quickstart guides).
3. Add or update images or demo GIFs.

**Example:**
- Add a new troubleshooting section to `README.md`
- Add `docs/marketing/clipboard_demo.gif`

---

### Platform-Specific Refactor or Fix

**Trigger:** When addressing platform-specific bugs, refactoring duplicated code, or improving OS integration  
**Command:** `/platform-fix`

1. Modify or refactor files with platform-specific suffixes (`_windows.go`, `_darwin.go`, `_unix.go`, `_other.go`).
2. Update or add tests for the affected platform.
3. Update build scripts or `Makefile` if needed.

**Example:**
```go
// internal/clipboard/clipboard_darwin.go
func copyToClipboardDarwin(data []byte) error {
    // macOS-specific implementation
}
```
---

## Testing Patterns

- Test files follow the pattern: `*_test.go`
- Tests are placed alongside implementation files in `cmd/cc-clip/` and `internal/` directories.
- The specific testing framework is not specified, but standard Go testing is implied.
- Example test file:
    ```go
    // internal/clipboard/clipboard_test.go
    import "testing"

    func TestCopyToClipboard(t *testing.T) {
        // test logic
    }
    ```

---

## Commands

| Command           | Purpose                                                        |
|-------------------|----------------------------------------------------------------|
| /new-feature      | Start a new feature with implementation, tests, and docs       |
| /windows-support  | Add or improve Windows-specific support                        |
| /bugfix           | Fix a bug and add/update a targeted test                       |
| /docs-update      | Enhance documentation or add troubleshooting info              |
| /platform-fix     | Refactor or fix platform-specific code (Windows, macOS, etc.)  |
```
