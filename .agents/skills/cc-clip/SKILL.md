```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill teaches the core development patterns and workflows used in the `cc-clip` Go codebase. It covers conventions for file naming, imports, exports, commit messages, and testing, as well as the primary workflow for implementing code changes alongside corresponding tests. Use this guide to contribute code that aligns with the project's established practices.

## Coding Conventions

### File Naming
- Use **snake_case** for all file names.
  - Example: `update.go`, `registry_test.go`

### Import Style
- Use **relative imports** within the project.
  - Example:
    ```go
    import "../internal/hosts"
    ```

### Export Style
- Use **named exports** for functions, types, and variables that should be accessible from other packages.
  - Example:
    ```go
    func NewRegistry() *Registry {
        // ...
    }
    ```

### Commit Messages
- Follow **conventional commit** patterns.
- Prefixes: `fix`, `docs`
- Example:
  ```
  fix: handle edge case in ssh shim for empty hostnames
  docs: update usage instructions in README
  ```

## Workflows

### Code Change with Corresponding Tests
**Trigger:** When modifying core logic and ensuring it is properly tested  
**Command:** `/code-with-tests`

1. Edit the relevant implementation file(s) to add new functionality or fix bugs.
   - Example: Modify `internal/hosts/registry.go` to update registry logic.
2. Edit or add the corresponding test file(s) to cover your changes.
   - Example: Update `internal/hosts/registry_test.go` to add tests for new cases.
3. Ensure that tests pass before committing your changes.
4. Use a conventional commit message, such as:
   ```
   fix: update registry logic to handle duplicate hosts
   ```
5. Submit your changes for review.

**Files commonly involved:**
- `cmd/cc-clip/update.go`
- `cmd/cc-clip/update_test.go`
- `internal/hosts/registry.go`
- `internal/hosts/registry_test.go`
- `internal/shim/ssh.go`
- `internal/shim/ssh_test.go`

## Testing Patterns

- Test files use the pattern: `*_test.go`
  - Example: `update_test.go`, `registry_test.go`
- Testing framework is not explicitly specified; standard Go testing is assumed.
- Place test files alongside their corresponding implementation files.
- Example test structure:
  ```go
  package hosts

  import "testing"

  func TestRegistry_AddHost(t *testing.T) {
      // test logic here
  }
  ```

## Commands

| Command           | Purpose                                                      |
|-------------------|--------------------------------------------------------------|
| /code-with-tests  | Start a code change workflow with corresponding test updates |
```
