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

// runClaudeNotify reads raw Claude hook JSON from stdin, injects the host alias
// (reproducing the cc-clip-hook bash flow, hook_template.go:24-31), and forwards
// it to /notify using the hook content type so the daemon classifies it. It is
// fail-soft: neither a stdin read error nor a POST failure propagates, matching
// the cc-clip-hook always-exit-0 contract for hook contexts.
func runClaudeNotify(port int, stdin io.Reader) error {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil // fail-soft: never block the hook (matches cc-clip-hook exit 0)
	}
	_ = postHookPayload(port, injectHost(raw)) // POST failure must not propagate
	return nil
}

// runCodexNotify reads the Codex notify JSON from stdin, parses it into a
// generic message, and posts it. This reproduces cmdNotify's
// --from-codex-stdin parse+post core. It is fail-soft: a read error, parse
// error, or POST failure must NOT propagate, since codex hook contexts require
// exit 0 (mirrors antigravity's non-blocking posture but without the
// decision-JSON stdout that codex hooks do not expect).
func runCodexNotify(port int, stdin io.Reader) error {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return nil
	}
	parsed, perr := parseCodexNotifyPayload(string(b))
	if perr != nil {
		return nil // fail-soft: invalid payload must not block the agent
	}
	_ = PostNotification(port, parsed)
	return nil
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
