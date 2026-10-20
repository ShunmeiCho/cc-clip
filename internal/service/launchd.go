//go:build darwin

package service

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	plistLabel    = "com.cc-clip.daemon"
	plistFileName = "com.cc-clip.daemon.plist"
)

// PlistPath returns the full path to the launchd plist file.
func PlistPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", "Library", "LaunchAgents", plistFileName)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistFileName)
}

// logPath returns the path for daemon log output.
func logPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", "Library", "Logs", "cc-clip.log")
	}
	return filepath.Join(home, "Library", "Logs", "cc-clip.log")
}

// generatePlist creates the launchd plist XML content.
// Includes an explicit PATH so Homebrew tools (pngpaste) are found
// even though launchd doesn't source the user's shell profile.
// serveArgs are extra `serve` flags (e.g. "--text", "off") appended after --port.
func generatePlist(binaryPath string, port int, serveArgs ...string) string {
	var extra strings.Builder
	for _, arg := range serveArgs {
		fmt.Fprintf(&extra, "\n        <string>%s</string>", html.EscapeString(arg))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
        <string>--port</string>
        <string>%d</string>%s
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, plistLabel, binaryPath, port, extra.String(), logPath(), logPath())
}

// launchctlLoad loads a plist via launchctl. Overridable for testing.
var launchctlLoad = func(plistPath string) error {
	cmd := exec.Command("launchctl", "load", "-w", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// launchctlUnload unloads a plist via launchctl. Overridable for testing.
var launchctlUnload = func(plistPath string) error {
	cmd := exec.Command("launchctl", "unload", "-w", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl unload failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// launchctlList checks if a job is loaded. Overridable for testing.
var launchctlList = func(label string) (bool, error) {
	cmd := exec.Command("launchctl", "list", label)
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// Install writes the plist file and loads the service via launchctl.
// serveArgs are extra `serve` flags baked into the plist; read them back with
// InstalledFlag before re-registering so they survive updates.
func Install(binaryPath string, port int, serveArgs ...string) error {
	plist := PlistPath()

	// Ensure LaunchAgents directory exists
	dir := filepath.Dir(plist)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create LaunchAgents directory: %w", err)
	}

	content := generatePlist(binaryPath, port, serveArgs...)
	if err := os.WriteFile(plist, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write plist: %w", err)
	}

	if err := launchctlLoad(plist); err != nil {
		// Clean up plist on load failure
		os.Remove(plist)
		return err
	}

	return nil
}

// launchctlRemove force-removes a job by label. Overridable for testing.
var launchctlRemove = func(label string) error {
	cmd := exec.Command("launchctl", "remove", label)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl remove failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Uninstall unloads the service and removes the plist file.
func Uninstall() error {
	plist := PlistPath()

	// Try unload via plist path first (requires file to exist).
	unloadErr := launchctlUnload(plist)

	// Fallback: remove by label (works even if plist is already deleted).
	if unloadErr != nil {
		_ = launchctlRemove(plistLabel)
	}

	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove plist: %w", err)
	}

	return nil
}

// Status checks if the launchd job is currently loaded/running.
func Status() (bool, error) {
	return launchctlList(plistLabel)
}

// InstalledPort reports the --port the installed plist starts the daemon on.
//
// `service install --port N` honours N, but every later re-registration read a
// hardcoded 18339 instead: `cc-clip update` unloaded a service running on N and
// reloaded it on 18339, which moved the daemon out from under every SSH tunnel
// forwarding N. Callers that re-register a service must read the port back from
// the plist they are about to replace.
func InstalledPort() (int, bool) {
	data, err := os.ReadFile(PlistPath())
	if err != nil {
		return 0, false
	}
	return parsePlistPort(string(data))
}

// parsePlistPort pulls the value that follows the "--port" argument out of the
// plist's ProgramArguments array.
//
// The array is located first, and XML comments are removed before anything is
// read. Scanning every <string> in the whole document meant a perfectly legal
// comment that happened to mention an older port was read as configuration,
// and the caller then MOVED the running service onto it — the exact failure
// this function exists to prevent, arrived at from the other direction.
func parsePlistPort(plist string) (int, bool) {
	args, ok := programArgumentsArray(stripXMLComments(plist))
	if !ok {
		return 0, false
	}
	values := plistStringValues(args)
	for i := 0; i+1 < len(values); i++ {
		if values[i] == "--port" {
			port, err := strconv.Atoi(values[i+1])
			if err != nil || port <= 0 || port > 65535 {
				return 0, false
			}
			return port, true
		}
	}
	return 0, false
}

// InstalledFlag reports the value of a `--name value` pair in the installed
// plist's ProgramArguments. Like InstalledPort, it lets re-registration keep
// flags the user chose at install time instead of silently resetting them.
func InstalledFlag(name string) (string, bool) {
	data, err := os.ReadFile(PlistPath())
	if err != nil {
		return "", false
	}
	return parsePlistFlag(string(data), name)
}

func parsePlistFlag(plist, name string) (string, bool) {
	args, ok := programArgumentsArray(stripXMLComments(plist))
	if !ok {
		return "", false
	}
	values := plistStringValues(args)
	for i := 0; i+1 < len(values); i++ {
		if values[i] == "--"+name {
			return html.UnescapeString(values[i+1]), true
		}
	}
	return "", false
}

// stripXMLComments removes <!-- ... --> spans. An unterminated comment makes
// the rest of the document unreadable, which is reported as "no port" rather
// than guessed at.
func stripXMLComments(doc string) string {
	var b strings.Builder
	for {
		start := strings.Index(doc, "<!--")
		if start < 0 {
			b.WriteString(doc)
			return b.String()
		}
		b.WriteString(doc[:start])
		rest := doc[start+len("<!--"):]
		end := strings.Index(rest, "-->")
		if end < 0 {
			return b.String()
		}
		doc = rest[end+len("-->"):]
	}
}

// programArgumentsArray returns the body of the <array> that follows the
// ProgramArguments key.
func programArgumentsArray(doc string) (string, bool) {
	key := strings.Index(doc, "<key>ProgramArguments</key>")
	if key < 0 {
		return "", false
	}
	rest := doc[key:]
	open := strings.Index(rest, "<array>")
	if open < 0 {
		return "", false
	}
	rest = rest[open+len("<array>"):]
	close := strings.Index(rest, "</array>")
	if close < 0 {
		return "", false
	}
	return rest[:close], true
}

func plistStringValues(plist string) []string {
	var out []string
	rest := plist
	for {
		start := strings.Index(rest, "<string>")
		if start < 0 {
			return out
		}
		rest = rest[start+len("<string>"):]
		end := strings.Index(rest, "</string>")
		if end < 0 {
			return out
		}
		out = append(out, rest[:end])
		rest = rest[end+len("</string>"):]
	}
}
