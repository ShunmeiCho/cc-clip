```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill teaches you how to contribute to the `cc-clip` Go codebase by following its established coding conventions and workflows. You'll learn file naming, import/export styles, commit message patterns, and how to implement features, expand tests, update documentation, and maintain build pipelines in a consistent, maintainable way.

## Coding Conventions

- **Language:** Go
- **Framework:** None detected
- **File Naming:** Use `snake_case` for all files.
  - Example: `x11_bridge.go`, `main_test.go`
- **Import Style:** Use relative imports.
  - Example:
    ```go
    import (
        "internal/daemon"
        "internal/tunnel"
    )
    ```
- **Export Style:** Use named exports (exported functions/types start with uppercase).
  - Example:
    ```go
    // Exported function
    func StartDaemon() error { ... }

    // Unexported function
    func startHelper() { ... }
    ```
- **Commit Messages:** Follow [Conventional Commits](https://www.conventionalcommits.org/) with prefixes like `fix`, `docs`, `feat`, `test`, `build`, `refactor`.
  - Example: `feat: add clipboard sync for X11 bridge`

## Workflows

### Feature Development & Implementation Tests
**Trigger:** When adding a new feature or refactoring a core component  
**Command:** `/feature-impl-test`

1. Edit or create implementation files in `internal/daemon/`, `internal/tunnel/`, `internal/x11bridge/`, or similar directories.
2. Edit or create corresponding `*_test.go` files for new or changed logic.
3. Optionally update related files in `internal/shim/` or `internal/plugin/`.
4. Commit changes with a conventional message, e.g., `feat: support new tunnel protocol`.
5. Run tests to ensure correctness.

**Example:**
```go
// internal/tunnel/tunnel.go
func NewTunnel() *Tunnel { ... }

// internal/tunnel/tunnel_test.go
func TestNewTunnel(t *testing.T) { ... }
```

### Documentation Update (Multilingual)
**Trigger:** When documenting a new feature, clarifying docs, or syncing translations  
**Command:** `/docs-update-multilingual`

1. Edit or add English documentation files: `README.md`, `docs/*.md`, `SECURITY.md`.
2. Edit or add translated documentation files: `README.zh-CN.md`, `README.ja.md`, etc.
3. Synchronize content and clarify differences across languages.
4. Commit with a message like `docs: update usage instructions in all languages`.

### Test Suite Expansion
**Trigger:** When expanding test coverage or adapting tests for new environments  
**Command:** `/expand-tests`

1. Edit or add `*_test.go` files across relevant `internal/` directories.
2. Update or create test helpers as needed.
3. Run the test suite to verify coverage.
4. Commit with a message like `test: add integration tests for daemon`.

**Example:**
```go
// internal/daemon/daemon_test.go
func TestStartDaemon(t *testing.T) { ... }
```

### Build or Installation Pipeline Update
**Trigger:** When updating build scripts, installer scripts, or CI/CD workflows  
**Command:** `/update-build-install`

1. Edit `.github/workflows/*.yml` for CI/CD changes.
2. Edit `Makefile` for build process updates.
3. Edit or add `scripts/install.ps1` and related test files for installation logic.
4. Commit with a message like `build: add Windows installer preflight checks`.

**Example:**
```yaml
# .github/workflows/ci.yml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - run: make build
```

## Testing Patterns

- **Test File Pattern:** All test files are named with the `_test.go` suffix.
  - Example: `daemon_test.go`, `install_windows_test.go`
- **Testing Framework:** Not explicitly specified; presumed to use Go's standard `testing` package.
- **Test Placement:** Tests are placed alongside implementation files in the same directory.
- **Test Example:**
    ```go
    // internal/x11bridge/x11bridge_test.go
    func TestBridgeInit(t *testing.T) {
        // test logic here
    }
    ```

## Commands

| Command                 | Purpose                                                         |
|-------------------------|-----------------------------------------------------------------|
| /feature-impl-test      | Start a new feature or refactor with corresponding tests        |
| /docs-update-multilingual | Update documentation in multiple languages                     |
| /expand-tests           | Add or update test files to improve coverage                    |
| /update-build-install   | Update build scripts, installer scripts, or CI/CD workflows     |
```