package shim

import (
	"fmt"
	"strings"
	"testing"
)

// mockExecutor simulates remote command execution for testing.
type mockExecutor struct {
	// files maps file paths to their content
	files map[string]string
	// shell is the value returned by echo $SHELL
	shell string
	// execLog records all commands executed
	execLog []string
}

func newMockExecutor(shell string) *mockExecutor {
	return &mockExecutor{
		files: make(map[string]string),
		shell: shell,
	}
}

func (m *mockExecutor) Exec(cmd string) (string, error) {
	m.execLog = append(m.execLog, cmd)

	// Handle echo $SHELL
	if cmd == "echo $SHELL" {
		return m.shell, nil
	}

	// Handle grep -F for marker detection
	if strings.HasPrefix(cmd, "grep -F") {
		// Extract the search string from grep -F "pattern" file
		for _, marker := range []string{displayMarkerStart, pathMarkerStart} {
			if strings.Contains(cmd, marker) {
				for _, content := range m.files {
					if strings.Contains(content, marker) {
						return marker, nil
					}
				}
				return "", nil
			}
		}
		return "", nil
	}

	// Handle touch + prepend (new format: prepend block before existing content)
	if strings.HasPrefix(cmd, "touch") && strings.Contains(cmd, "cc-clip-tmp") {
		rcFile := extractRCFileFromPrepend(cmd)
		// Determine which block is being prepended by checking command content
		var block string
		if strings.Contains(cmd, displayMarkerStart) {
			block = displayBlock()
		} else {
			block = pathBlock()
		}
		existing := m.files[rcFile]
		m.files[rcFile] = block + "\n" + existing
		return "", nil
	}

	// Handle touch + printf >> (legacy append to rc file)
	if strings.HasPrefix(cmd, "touch") && strings.Contains(cmd, ">> ") {
		rcFile := extractRCFile(cmd)
		block := extractPrintfArg(cmd)
		existing := m.files[rcFile]
		m.files[rcFile] = existing + "\n" + block
		return "", nil
	}

	// Handle sed -i for removal
	if strings.HasPrefix(cmd, "sed -i") {
		rcFile := extractSedFile(cmd)
		content, exists := m.files[rcFile]
		if !exists {
			return "", nil
		}
		// Determine which marker block to remove
		markerStart := pathMarkerStart
		markerEnd := pathMarkerEnd
		if strings.Contains(cmd, sedEscape(displayMarkerStart)) {
			markerStart = displayMarkerStart
			markerEnd = displayMarkerEnd
		}
		// Simulate sed removal of marker block
		lines := strings.Split(content, "\n")
		var result []string
		inBlock := false
		for _, line := range lines {
			if strings.Contains(line, markerStart) {
				inBlock = true
				continue
			}
			if inBlock && strings.Contains(line, markerEnd) {
				inBlock = false
				continue
			}
			if !inBlock {
				result = append(result, line)
			}
		}
		m.files[rcFile] = strings.Join(result, "\n")
		return "", nil
	}

	return "", nil
}

// extractRCFileFromPrepend extracts the rc file from the prepend command.
// Format: touch ~/.bashrc && { printf '%s\n' "..."; cat ~/.bashrc; } > ~/.bashrc.cc-clip-tmp && mv ...
func extractRCFileFromPrepend(cmd string) string {
	// Look for "touch <file>" at the start
	parts := strings.Fields(cmd)
	if len(parts) >= 2 {
		return parts[1]
	}
	return "~/.bashrc"
}

// extractRCFile extracts the rc file path from commands like:
// touch ~/.bashrc && printf '\n%s' "..." >> ~/.bashrc
func extractRCFile(cmd string) string {
	// Find the last token after ">>"
	parts := strings.Split(cmd, ">> ")
	if len(parts) < 2 {
		return "~/.bashrc"
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// extractPrintfArg extracts the content from printf '%s' "content" commands.
func extractPrintfArg(cmd string) string {
	// The block is passed as a quoted argument to printf
	return pathBlock()
}

// extractSedFile extracts the file path from a sed -i command.
func extractSedFile(cmd string) string {
	// Pattern: sed -i.cc-clip-bak '...' <file> ...
	parts := strings.Fields(cmd)
	for i, p := range parts {
		if strings.HasSuffix(p, "rc") && i > 0 {
			return p
		}
	}
	// Fallback: look for ~/.bashrc or ~/.zshrc
	if strings.Contains(cmd, "~/.zshrc") {
		return "~/.zshrc"
	}
	return "~/.bashrc"
}

func TestDetectRemoteShellBash(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	shell, err := DetectRemoteShellSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shell != "bash" {
		t.Fatalf("expected bash, got %s", shell)
	}
}

func TestDetectRemoteShellZsh(t *testing.T) {
	m := newMockExecutor("/usr/bin/zsh")

	shell, err := DetectRemoteShellSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shell != "zsh" {
		t.Fatalf("expected zsh, got %s", shell)
	}
}

func TestDetectRemoteShellUnknownDefaultsBash(t *testing.T) {
	m := newMockExecutor("/usr/local/bin/fish")

	shell, err := DetectRemoteShellSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shell != "bash" {
		t.Fatalf("expected bash fallback for unknown shell, got %s", shell)
	}
}

func TestDetectRemoteShellError(t *testing.T) {
	m := &errorExecutor{err: fmt.Errorf("connection refused")}

	_, err := DetectRemoteShellSession(m)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to detect remote shell") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRCFilePath(t *testing.T) {
	tests := []struct {
		shell    string
		expected string
	}{
		{"bash", "~/.bashrc"},
		{"zsh", "~/.zshrc"},
		{"fish", "~/.bashrc"}, // unknown defaults to bashrc
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			result := RCFilePath(tt.shell)
			if result != tt.expected {
				t.Fatalf("RCFilePath(%q) = %q, want %q", tt.shell, result, tt.expected)
			}
		})
	}
}

func TestFixRemotePathInjectsBashrc(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	err := FixRemotePathSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := m.files["~/.bashrc"]
	if !strings.Contains(content, pathMarkerStart) {
		t.Fatal("marker start not found in bashrc")
	}
	if !strings.Contains(content, pathExport) {
		t.Fatal("PATH export not found in bashrc")
	}
	if !strings.Contains(content, pathMarkerEnd) {
		t.Fatal("marker end not found in bashrc")
	}
}

func TestFixRemotePathInjectsZshrc(t *testing.T) {
	m := newMockExecutor("/bin/zsh")

	err := FixRemotePathSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := m.files["~/.zshrc"]
	if !strings.Contains(content, pathMarkerStart) {
		t.Fatal("marker start not found in zshrc")
	}
	if !strings.Contains(content, pathExport) {
		t.Fatal("PATH export not found in zshrc")
	}
}

func TestFixRemotePathIdempotent(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// First injection
	if err := FixRemotePathSession(m); err != nil {
		t.Fatalf("first fix failed: %v", err)
	}

	execCountAfterFirst := len(m.execLog)

	// Second injection should be a no-op
	if err := FixRemotePathSession(m); err != nil {
		t.Fatalf("second fix failed: %v", err)
	}

	// Verify no additional touch/printf command was run
	// (only echo $SHELL and grep should run on the second call)
	secondCallCmds := m.execLog[execCountAfterFirst:]
	for _, cmd := range secondCallCmds {
		if strings.HasPrefix(cmd, "touch") {
			t.Fatal("idempotent fix should not append again")
		}
	}

	// Verify content only has one marker block
	content := m.files["~/.bashrc"]
	count := strings.Count(content, pathMarkerStart)
	if count != 1 {
		t.Fatalf("expected exactly 1 marker block, found %d", count)
	}
}

func TestIsPathFixed(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// Before fix
	fixed, err := IsPathFixedSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fixed {
		t.Fatal("should not be fixed before injection")
	}

	// After fix
	if err := FixRemotePathSession(m); err != nil {
		t.Fatalf("fix failed: %v", err)
	}

	fixed, err = IsPathFixedSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fixed {
		t.Fatal("should be fixed after injection")
	}
}

func TestRemoveRemotePath(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// Inject first
	if err := FixRemotePathSession(m); err != nil {
		t.Fatalf("fix failed: %v", err)
	}

	// Verify injected
	content := m.files["~/.bashrc"]
	if !strings.Contains(content, pathMarkerStart) {
		t.Fatal("marker not found after injection")
	}

	// Remove
	if err := RemoveRemotePathSession(m); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	// Verify removed
	content = m.files["~/.bashrc"]
	if strings.Contains(content, pathMarkerStart) {
		t.Fatal("marker should be removed")
	}
	if strings.Contains(content, pathExport) {
		t.Fatal("PATH export should be removed")
	}
	if strings.Contains(content, pathMarkerEnd) {
		t.Fatal("marker end should be removed")
	}
}

func TestRemoveRemotePathNoFile(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// Remove without prior injection should not error
	err := RemoveRemotePathSession(m)
	if err != nil {
		t.Fatalf("remove on non-existent should not error: %v", err)
	}
}

func TestPathBlock(t *testing.T) {
	block := pathBlock()

	if !strings.HasPrefix(block, pathMarkerStart) {
		t.Fatal("block should start with marker")
	}
	if !strings.Contains(block, pathExport) {
		t.Fatal("block should contain PATH export")
	}
	if !strings.HasSuffix(block, pathMarkerEnd+"\n") {
		t.Fatal("block should end with marker")
	}
}

func TestSedEscape(t *testing.T) {
	escaped := sedEscape("# >>> cc-clip PATH (do not edit) >>>")
	if strings.Contains(escaped, "(") && !strings.Contains(escaped, `\(`) {
		t.Fatal("parentheses should be escaped")
	}
}

// --- DISPLAY marker tests ---

func TestDisplayBlockContainsMarkers(t *testing.T) {
	block := displayBlock()

	if !strings.Contains(block, displayMarkerStart) {
		t.Fatal("displayBlock should contain marker start")
	}
	if !strings.Contains(block, displayMarkerEnd) {
		t.Fatal("displayBlock should contain marker end")
	}
}

func TestDisplayBlockContainsDISPLAYLogic(t *testing.T) {
	block := displayBlock()

	expectedSnippets := []string{
		`DISPLAY`,
		`$HOME/.cache/cc-clip/codex/display`,
		`_cc_clip_display`,
		`_cc_clip_num`,
		`export DISPLAY="127.0.0.1:${_cc_clip_num}"`,
		`unset _cc_clip_display _cc_clip_num`,
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(block, snippet) {
			t.Errorf("displayBlock should contain %q", snippet)
		}
	}

	// Verify Unix socket check is NOT present (we use TCP now)
	if strings.Contains(block, `/tmp/.X11-unix/X`) {
		t.Error("displayBlock should not check Unix socket path; TCP loopback is used for Codex sandbox compatibility")
	}
}

func TestIsDisplayFixedSession(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// Before fix
	fixed, err := IsDisplayFixedSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fixed {
		t.Fatal("should not be fixed before injection")
	}

	// After fix
	if err := FixDisplaySession(m); err != nil {
		t.Fatalf("fix failed: %v", err)
	}

	fixed, err = IsDisplayFixedSession(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fixed {
		t.Fatal("should be fixed after injection")
	}
}

func TestFixDisplaySessionIdempotent(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// First injection
	if err := FixDisplaySession(m); err != nil {
		t.Fatalf("first fix failed: %v", err)
	}

	execCountAfterFirst := len(m.execLog)

	// Second injection should be a no-op
	if err := FixDisplaySession(m); err != nil {
		t.Fatalf("second fix failed: %v", err)
	}

	// Verify no additional touch/printf command was run
	secondCallCmds := m.execLog[execCountAfterFirst:]
	for _, cmd := range secondCallCmds {
		if strings.HasPrefix(cmd, "touch") {
			t.Fatal("idempotent fix should not prepend again")
		}
	}

	// Verify content only has one marker block
	content := m.files["~/.bashrc"]
	count := strings.Count(content, displayMarkerStart)
	if count != 1 {
		t.Fatalf("expected exactly 1 DISPLAY marker block, found %d", count)
	}
}

func TestRemoveDisplayMarkerSessionPreservesPATH(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// Inject both PATH and DISPLAY markers
	if err := FixRemotePathSession(m); err != nil {
		t.Fatalf("PATH fix failed: %v", err)
	}
	if err := FixDisplaySession(m); err != nil {
		t.Fatalf("DISPLAY fix failed: %v", err)
	}

	content := m.files["~/.bashrc"]
	if !strings.Contains(content, pathMarkerStart) {
		t.Fatal("PATH marker should be present before removal")
	}
	if !strings.Contains(content, displayMarkerStart) {
		t.Fatal("DISPLAY marker should be present before removal")
	}

	// Remove only DISPLAY marker
	if err := RemoveDisplayMarkerSession(m); err != nil {
		t.Fatalf("DISPLAY remove failed: %v", err)
	}

	content = m.files["~/.bashrc"]
	if strings.Contains(content, displayMarkerStart) {
		t.Fatal("DISPLAY marker should be removed")
	}
	if strings.Contains(content, displayMarkerEnd) {
		t.Fatal("DISPLAY marker end should be removed")
	}
	if !strings.Contains(content, pathMarkerStart) {
		t.Fatal("PATH marker should still be present after DISPLAY removal")
	}
	if !strings.Contains(content, pathExport) {
		t.Fatal("PATH export should still be present after DISPLAY removal")
	}
}

func TestRemoveDisplayMarkerSessionIdempotent(t *testing.T) {
	m := newMockExecutor("/bin/bash")

	// Inject DISPLAY marker
	if err := FixDisplaySession(m); err != nil {
		t.Fatalf("fix failed: %v", err)
	}

	// Remove twice — second should be a no-op, no error
	if err := RemoveDisplayMarkerSession(m); err != nil {
		t.Fatalf("first remove failed: %v", err)
	}
	if err := RemoveDisplayMarkerSession(m); err != nil {
		t.Fatalf("second remove failed: %v", err)
	}

	content := m.files["~/.bashrc"]
	if strings.Contains(content, displayMarkerStart) {
		t.Fatal("DISPLAY marker should be removed")
	}
}

// errorExecutor always returns an error.
type errorExecutor struct {
	err error
}

func (e *errorExecutor) Exec(cmd string) (string, error) {
	return "", e.err
}
