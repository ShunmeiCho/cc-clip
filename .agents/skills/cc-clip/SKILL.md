```markdown
# cc-clip Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill covers the development patterns and workflows for the `cc-clip` Go codebase. It details coding conventions, commit patterns, testing approaches, and step-by-step instructions for key maintenance workflows. The repository focuses on remote environment detection, with robust error handling and testing for edge cases, especially in minimal or non-standard SSH/container setups.

## Coding Conventions

- **File Naming:**  
  Use `snake_case` for all file names.  
  _Example:_  
  ```
  main.go
  n0_test.go
  v070_probe_test.go
  ```

- **Import Style:**  
  Use relative imports within the module.  
  _Example:_  
  ```go
  import "../internal/shim"
  ```

- **Export Style:**  
  Use named exports for functions, types, and variables.  
  _Example:_  
  ```go
  func DetectRemoteEnv() error { ... }
  ```

- **Commit Messages:**  
  Follow [Conventional Commits](https://www.conventionalcommits.org/) with the `fix` prefix for bug fixes.  
  _Example:_  
  ```
  fix: handle error in remote detection for minimal containers
  ```

## Workflows

### Harden and Fix Remote Detection
**Trigger:** When you need to improve or fix the detection of remote environments, especially to handle edge cases or failures in minimal container images or non-standard SSH environments.  
**Command:** `/harden-remote-detection`

1. **Update detection logic in the main command file**  
   Edit `cmd/cc-clip/main.go` to improve the robustness of remote environment detection.  
   _Example:_  
   ```go
   // Before
   err := shim.CheckRemote()
   // After
   err := shim.SafeCheckRemote()
   if err != nil {
       log.Printf("Remote detection failed: %v", err)
   }
   ```

2. **Modify or extend error handling for detection failures**  
   Ensure all edge cases and errors are handled gracefully, providing clear logs or fallback behaviors.

3. **Add or update tests for new detection logic or edge cases**  
   Update or create test files such as `cmd/cc-clip/n0_test.go` and `internal/shim/v070_probe_test.go` to cover new scenarios.  
   _Example:_  
   ```go
   func TestSafeCheckRemote_MinimalContainer(t *testing.T) {
       // Simulate minimal container environment
       err := SafeCheckRemote()
       if err == nil {
           t.Error("Expected error in minimal container, got nil")
       }
   }
   ```

4. **Update internal SSH or probe utilities if needed**  
   Refactor or extend files like `internal/shim/ssh.go` to support new detection logic or handle additional edge cases.

## Testing Patterns

- **Test File Naming:**  
  Test files follow the pattern `*_test.go` and are placed alongside the code they test.  
  _Example:_  
  ```
  n0_test.go
  v070_probe_test.go
  ```

- **Framework:**  
  The testing framework is not explicitly specified, but standard Go testing (`testing` package) is assumed.

- **Test Example:**  
  ```go
  func TestDetectionLogic(t *testing.T) {
      result := DetectRemoteEnv()
      if result != nil {
          t.Errorf("Expected nil, got %v", result)
      }
  }
  ```

## Commands

| Command                  | Purpose                                                          |
|--------------------------|------------------------------------------------------------------|
| /harden-remote-detection | Harden and fix remote environment detection logic and add tests.  |
```
