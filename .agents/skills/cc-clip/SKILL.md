```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill covers the development patterns and conventions used in the `cc-clip` Go repository. It provides guidance on file organization, code style, commit practices, and testing patterns, enabling contributors to maintain consistency and quality across the codebase.

## Coding Conventions

### File Naming
- Use **snake_case** for all file names.
  - Example: `image_utils.go`, `clip_processor.go`

### Import Style
- Use **relative imports** for internal packages.
  - Example:
    ```go
    import "./utils"
    ```

### Export Style
- Use **named exports** for functions, types, and variables.
  - Example:
    ```go
    // In utils.go
    package utils

    func ProcessClip(input string) error {
        // implementation
    }
    ```

### Commit Message Style
- Follow **conventional commits** with the `feat` prefix for new features.
  - Example:
    ```
    feat: add support for batch clip processing
    ```

## Workflows

### Feature Development
**Trigger:** When adding a new feature  
**Command:** `/feature-development`

1. Create a new branch for your feature.
2. Implement your feature using snake_case file naming and named exports.
3. Use relative imports for internal packages.
4. Write or update tests in files matching `*.test.*`.
5. Commit your changes using the `feat` prefix:
    ```
    feat: brief description of the feature
    ```
6. Open a pull request for review.

### Testing
**Trigger:** When verifying code correctness  
**Command:** `/run-tests`

1. Identify or create test files matching the pattern `*.test.*`.
2. Write tests for your new or updated code.
3. Run the tests using your preferred Go testing tool (framework unknown).
4. Review test results and fix any failures.

## Testing Patterns

- Test files follow the `*.test.*` naming convention.
  - Example: `clip_processor.test.go`
- Testing framework is not specified; use standard Go testing practices.
- Place test functions in the same package as the code under test.

  ```go
  // clip_processor.test.go
  package clip

  import "testing"

  func TestProcessClip(t *testing.T) {
      // test implementation
  }
  ```

## Commands
| Command                | Purpose                                      |
|------------------------|----------------------------------------------|
| /feature-development   | Start the workflow for developing a feature  |
| /run-tests             | Run and verify tests in the codebase         |
```
