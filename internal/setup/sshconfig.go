package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SSHConfigChange describes a modification made to ~/.ssh/config.
type SSHConfigChange struct {
	Action string // "created", "added", "ok"
	Detail string
}

// EnsureSSHConfig ensures ~/.ssh/config has required directives for cc-clip:
//   - RemoteForward <port> 127.0.0.1:<port>
//   - ControlMaster no
//   - ControlPath none
//
// If the host block doesn't exist, it is created before "Host *".
// A backup is written to ~/.ssh/config.cc-clip-backup before any modification.
func EnsureSSHConfig(host string, port int) ([]SSHConfigChange, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create ~/.ssh: %w", err)
	}
	return ensureSSHConfigAt(filepath.Join(sshDir, "config"), host, port)
}

func ensureSSHConfigAt(configPath string, host string, port int) ([]SSHConfigChange, error) {
	content, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read %s: %w", configPath, err)
	}

	lines := strings.Split(string(content), "\n")
	block := findHostBlock(lines, host)
	rfValue := fmt.Sprintf("%d 127.0.0.1:%d", port, port)
	var changes []SSHConfigChange
	modified := false

	if block == nil {
		newBlock := []string{
			fmt.Sprintf("Host %s", host),
			fmt.Sprintf("    RemoteForward %s", rfValue),
			"    ControlMaster no",
			"    ControlPath none",
			"",
		}
		starLine := findHostStarLine(lines)
		if starLine >= 0 {
			result := make([]string, 0, len(lines)+len(newBlock))
			result = append(result, lines[:starLine]...)
			result = append(result, newBlock...)
			result = append(result, lines[starLine:]...)
			lines = result
		} else {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, newBlock...)
		}
		changes = append(changes, SSHConfigChange{"created", fmt.Sprintf("Host %s (RemoteForward, ControlMaster no, ControlPath none)", host)})
		modified = true
	} else {
		type required struct {
			key      string
			value    string
			contains string
		}
		directives := []required{
			{"RemoteForward", rfValue, fmt.Sprintf("%d", port)},
			{"ControlMaster", "no", "no"},
			{"ControlPath", "none", "none"},
		}
		for _, d := range directives {
			if block.hasDirective(strings.ToLower(d.key), d.contains) {
				changes = append(changes, SSHConfigChange{"ok", fmt.Sprintf("%s %s", d.key, d.value)})
			} else {
				line := fmt.Sprintf("    %s %s", d.key, d.value)
				lines = insertDirectiveInBlock(lines, block, line)
				block.endLine++
				changes = append(changes, SSHConfigChange{"added", fmt.Sprintf("%s %s", d.key, d.value)})
				modified = true
			}
		}
	}

	if modified {
		if len(content) > 0 {
			backupPath := configPath + ".cc-clip-backup"
			_ = os.WriteFile(backupPath, content, 0644)
		}
		newContent := strings.Join(lines, "\n")
		if err := os.WriteFile(configPath, []byte(newContent), 0644); err != nil {
			return nil, fmt.Errorf("cannot write %s: %w", configPath, err)
		}
	}

	return changes, nil
}

type sshBlock struct {
	startLine  int
	endLine    int
	directives []sshDirective
}

type sshDirective struct {
	key   string // lowercase
	value string
}

func (b *sshBlock) hasDirective(key, valueSubstr string) bool {
	for _, d := range b.directives {
		if d.key == key && strings.Contains(strings.ToLower(d.value), strings.ToLower(valueSubstr)) {
			return true
		}
	}
	return false
}

func findHostBlock(lines []string, host string) *sshBlock {
	var block *sshBlock
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if matchesHost(trimmed, host) {
			block = &sshBlock{startLine: i}
			continue
		}
		if block != nil && isAnyHostLine(trimmed) {
			block.endLine = i
			return block
		}
		if block != nil {
			key, val := parseSSHDirective(trimmed)
			if key != "" {
				block.directives = append(block.directives, sshDirective{
					key:   strings.ToLower(key),
					value: val,
				})
			}
		}
	}
	if block != nil {
		block.endLine = len(lines)
	}
	return block
}

func matchesHost(trimmed, host string) bool {
	if !isAnyHostLine(trimmed) {
		return false
	}
	for _, f := range strings.Fields(trimmed)[1:] {
		if f == host {
			return true
		}
	}
	return false
}

func isAnyHostLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "Host ") || strings.HasPrefix(trimmed, "Host\t")
}

func findHostStarLine(lines []string) int {
	for i, line := range lines {
		if matchesHost(strings.TrimSpace(line), "*") {
			return i
		}
	}
	return -1
}

func parseSSHDirective(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", ""
	}
	// Handle both "Key Value" and "Key=Value"
	trimmed = strings.Replace(trimmed, "=", " ", 1)
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) < 2 {
		return parts[0], ""
	}
	return parts[0], strings.TrimSpace(parts[1])
}

func insertDirectiveInBlock(lines []string, block *sshBlock, directive string) []string {
	insertAt := block.endLine
	for insertAt > block.startLine+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:insertAt]...)
	result = append(result, directive)
	result = append(result, lines[insertAt:]...)
	return result
}
