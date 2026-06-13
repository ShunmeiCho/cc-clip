```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill covers the core development and contribution patterns for the `cc-clip` repository, a Go-based project focused on clipboard and tunneling utilities. It details coding conventions, commit practices, and the main workflows for feature development, refactoring, and documentation updates. The guide is designed to help new and existing contributors quickly align with the project's standards and processes.

## Coding Conventions

### File Naming
- Use **snake_case** for all file names.
  - Example: `clipboard_windows.go`, `server_test.go`

### Imports
- Use **relative imports** within the module.
  - Example:
    ```go
    import (
        "fmt"
        "os"
        "cc-clip/internal/daemon"
    )
    ```

### Exports
- Use **named exports** for functions, types, and variables that need to be accessed outside their package.
  - Example:
    ```go
    // Exported function
    func StartServer() error {
        // ...
    }
    ```

### Commit Messages
- Follow **conventional commit** style with these prefixes: `docs`, `feat`, `fix`, `test`, `build`, `refactor`
- Keep commit messages concise (average ~50 characters).
  - Example: `feat: add clipboard listener for Windows`

## Workflows

### Feature Development with Tests
**Trigger:** When adding a new feature or refactoring a subsystem, ensuring it is covered by tests.  
**Command:** `/new-feature`

1. Edit or add implementation files in `internal/*/*.go`.
2. Edit or add corresponding test files in `internal/*/*_test.go`.
3. Ensure all new or modified code is covered by tests.
4. Run tests locally to verify correctness.
5. Commit changes using the conventional commit style.
6. Open a pull request for review.

**Example:**
```go
// internal/daemon/server.go
func StartServer() error {
    // implementation
}

// internal/daemon/server_test.go
func TestStartServer(t *testing.T) {
    err := StartServer()
    if err != nil {
        t.Fatal(err)
    }
}
```

### Documentation Update Multilingual
**Trigger:** When updating documentation for new features, clarifications, or translations.  
**Command:** `/update-docs`

1. Edit main documentation files such as `README.md`, `SECURITY.md`, or files in `docs/*.md`.
2. Update translated documentation files as needed (e.g., `README.ja.md`, `README.zh-CN.md`).
3. Ensure documentation changes reflect the latest features or clarifications.
4. Commit changes with a `docs:` prefix in the commit message.
5. Open a pull request for review.

**Example:**
```markdown
# cc-clip

A cross-platform clipboard utility with tunneling support.
```

## Testing Patterns

- Test files are named with the pattern `*_test.go` and reside alongside their implementation files.
- The testing framework is not explicitly specified, but Go's standard `testing` package is likely used.
- Example test file structure:
    ```go
    // internal/tunnel/fetch_test.go
    import "testing"

    func TestFetchTunnel(t *testing.T) {
        // test logic here
    }
    ```

## Commands

| Command        | Purpose                                                      |
|----------------|--------------------------------------------------------------|
| /new-feature   | Start a new feature or refactor with corresponding tests     |
| /update-docs   | Update documentation, including multilingual files           |
```
