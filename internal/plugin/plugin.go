package plugin

import (
	"fmt"
	"io"
)

// Adapter names dispatched by Run.
const (
	AdapterClaudeNotify      = "claude-notify"
	AdapterCodexNotify       = "codex-notify"
	AdapterAntigravityNotify = "antigravity-notify"
)

// Run dispatches to the named adapter handler. stdin/stdout are injected for
// testability. port comes from the caller (the cmd layer resolves it from
// getPort()).
func Run(name string, port int, stdin io.Reader, stdout io.Writer) error {
	switch name {
	case AdapterClaudeNotify:
		return runClaudeNotify(port, stdin)
	case AdapterCodexNotify:
		return runCodexNotify(port, stdin)
	case AdapterAntigravityNotify:
		return runAntigravityNotify(port, stdin, stdout)
	default:
		return fmt.Errorf("unknown plugin adapter: %q", name)
	}
}

// runClaudeNotify reads raw Claude hook JSON from stdin and forwards it
// unchanged to /notify using the hook content type, reproducing the
// cc-clip-hook bash flow's payload semantics (the daemon classifies the raw
// hook JSON). Host injection stays the bash script's job in the deployed path.
func runClaudeNotify(port int, stdin io.Reader) error {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("failed to read hook payload from stdin: %w", err)
	}
	return postHookPayload(port, raw)
}

// runCodexNotify reads the Codex notify JSON from stdin, parses it into a
// generic message, and posts it. This reproduces cmdNotify's
// --from-codex-stdin parse+post core.
func runCodexNotify(port int, stdin io.Reader) error {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("failed to read codex payload from stdin: %w", err)
	}
	parsed, err := parseCodexNotifyPayload(string(b))
	if err != nil {
		return fmt.Errorf("invalid codex notify payload: %w", err)
	}
	return PostNotification(port, parsed)
}

// runAntigravityNotify parses stdin as a codex-style payload and posts the
// notification, but ALWAYS writes {"decision":""} to stdout on every exit path
// (success or POST failure) and returns nil regardless of POST outcome, so the
// dispatcher never blocks 'agy' from stopping.
func runAntigravityNotify(port int, stdin io.Reader, stdout io.Writer) error {
	defer func() { _, _ = io.WriteString(stdout, "{\"decision\":\"\"}\n") }()
	b, err := io.ReadAll(stdin)
	if err != nil {
		return nil // stdout already guaranteed by defer
	}
	parsed, perr := parseCodexNotifyPayload(string(b))
	if perr == nil {
		_ = PostNotification(port, parsed)
	}
	return nil
}
