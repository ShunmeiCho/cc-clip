```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill teaches you the core development patterns and conventions used in the `cc-clip` repository, a Go codebase. You'll learn how to structure files, write imports and exports, follow commit message conventions, and implement and run tests. This guide is ideal for contributors looking to maintain consistency and quality in the project.

## Coding Conventions

### File Naming
- Use **snake_case** for all file names.
  - Example: `clip_manager.go`, `utils_test.go`

### Import Style
- Use **relative imports** within the project.
  - Example:
    ```go
    import "../utils"
    ```

### Export Style
- Use **named exports** for functions, types, and variables.
  - Example:
    ```go
    // In clip_manager.go
    package clip

    func ExportedFunction() {
        // implementation
    }
    ```

### Commit Messages
- Follow **conventional commit** format.
- Allowed prefixes: `fix`, `docs`, `feat`
- Average commit message length: ~56 characters
  - Example:
    ```
    feat: add support for multiple clipboard formats
    fix: resolve panic on empty clipboard content
    docs: update README with installation instructions
    ```

## Workflows

### Creating a New Feature
**Trigger:** When adding new functionality  
**Command:** `/new-feature`

1. Create a new file using snake_case if needed.
2. Implement the feature using named exports.
3. Use relative imports for internal dependencies.
4. Write or update tests in a corresponding `*.test.*` file.
5. Commit with a message starting with `feat:`.
6. Open a pull request.

### Fixing a Bug
**Trigger:** When resolving a bug or issue  
**Command:** `/bugfix`

1. Identify the bug and locate the relevant code.
2. Apply the fix, maintaining code style conventions.
3. Update or add tests to cover the fix.
4. Commit with a message starting with `fix:`.
5. Open a pull request.

### Updating Documentation
**Trigger:** When improving or correcting documentation  
**Command:** `/update-docs`

1. Edit or add documentation files as needed.
2. Commit with a message starting with `docs:`.
3. Open a pull request.

## Testing Patterns

- Test files follow the pattern: `*.test.*`
  - Example: `clip_manager.test.go`
- The specific testing framework is not detected, but standard Go testing conventions likely apply.
- Place tests in the same package or a dedicated test file.
- Example test file structure:
    ```go
    // clip_manager.test.go
    package clip

    import "testing"

    func TestExportedFunction(t *testing.T) {
        // test implementation
    }
    ```

## Commands
| Command        | Purpose                                         |
|----------------|-------------------------------------------------|
| /new-feature   | Start the workflow for adding a new feature     |
| /bugfix        | Begin the process for fixing a bug              |
| /update-docs   | Initiate documentation updates                  |
```
