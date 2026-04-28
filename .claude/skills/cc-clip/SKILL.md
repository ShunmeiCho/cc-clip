```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview

This skill teaches how to contribute to the `cc-clip` Go codebase by following its established development patterns, coding conventions, and workflows. You'll learn how to implement features, fix bugs, extend platform support, enhance documentation, and manage demo media, all while adhering to the project's style and structure.

## Coding Conventions

**File Naming**
- Use `snake_case` for file names.
  - Example: `clipboard_utils.go`, `windows_support_test.go`

**Import Style**
- Prefer **relative imports** within the module.
  - Example:
    ```go
    import (
        "internal/clipboard"
        "internal/utils"
    )
    ```

**Export Style**
- Use **named exports** for functions, types, and variables that should be accessible outside their package.
  - Example:
    ```go
    // In internal/clipboard/clipboard.go
    package clipboard

    // Exported function
    func Copy(text string) error {
        // ...
    }

    // Unexported (private) function
    func parseClipboardData(data []byte) string {
        // ...
    }
    ```

**Commit Messages**
- Follow [Conventional Commits](https://www.conventionalcommits.org/) with these prefixes:
  - `feat`: New features
  - `fix`: Bug fixes
  - `docs`: Documentation updates
- Average commit message length: ~57 characters.
  - Example: `feat: add Windows clipboard integration`

## Workflows

### Feature Development with Tests and Docs
**Trigger:** When adding a significant new feature or subsystem  
**Command:** `/new-feature`

1. Implement core feature logic in main and internal source files.
   - Example: Edit `cmd/cc-clip/main.go` and add new files in `internal/`.
2. Add or update corresponding test files.
   - Example: Create or update `internal/feature_name_test.go`.
3. Write or update design or plan documentation in `docs/plans/`.
   - Example: `docs/plans/new_feature.md`
4. Update `README.md` or other user-facing docs if needed.

---

### Platform Support Extension: Windows
**Trigger:** When adding or enhancing Windows compatibility  
**Command:** `/add-windows-support`

1. Add or modify Windows-specific implementation files (suffix `_windows.go`).
   - Example: `internal/clipboard_windows.go`
2. Add or update Windows-specific tests (suffix `_windows_test.go`).
   - Example: `internal/clipboard_windows_test.go`
3. Update `Makefile` and `.goreleaser.yaml` for Windows targets.
4. Update `README.md` and/or `docs/windows-quickstart.md`.

---

### Bugfix with Targeted Test
**Trigger:** When fixing a bug and preventing regressions  
**Command:** `/fix-bug`

1. Fix the bug in the relevant implementation file(s).
   - Example: Edit `internal/clipboard.go`
2. Add or update a test file to cover the fixed behavior.
   - Example: Update or create `internal/clipboard_test.go`

---

### Documentation Enhancement or Troubleshooting Update
**Trigger:** When clarifying setup, usage, or troubleshooting steps  
**Command:** `/update-docs`

1. Edit `README.md` to add or clarify instructions, troubleshooting, or diagrams.
2. Optionally, add or update `docs/plans/*.md` or `docs/windows-quickstart.md`.

---

### Demo Media Update
**Trigger:** When improving or adding demo visuals for the project  
**Command:** `/add-demo-gif`

1. Generate or update GIF files in `docs/marketing/`.
2. Add or update `.tape` source files and simulation scripts.
3. Update `README.md` to reference new/updated GIFs.

---

### Design or Implementation Plan Documentation
**Trigger:** When documenting a new architectural or feature plan  
**Command:** `/new-design-doc`

1. Create a new markdown file in `docs/plans/` describing the design or implementation plan.
2. Optionally, reference the plan in `README.md` or related feature commits.

---

## Testing Patterns

- Test files follow the pattern `*_test.go` and are located alongside source files.
  - Example: `internal/clipboard_test.go`
- Testing framework is not explicitly specified, but standard Go testing is assumed.
  - Example test:
    ```go
    package clipboard

    import "testing"

    func TestCopy(t *testing.T) {
        err := Copy("test")
        if err != nil {
            t.Errorf("Copy failed: %v", err)
        }
    }
    ```

## Commands

| Command              | Purpose                                                        |
|----------------------|----------------------------------------------------------------|
| /new-feature         | Start a new feature with implementation, tests, and docs       |
| /add-windows-support | Add or improve Windows platform support                        |
| /fix-bug             | Fix a bug and add/update a targeted test                       |
| /update-docs         | Enhance documentation or troubleshooting guides                |
| /add-demo-gif        | Add or update demo GIFs and related media                      |
| /new-design-doc      | Add a new design or implementation plan document               |
```