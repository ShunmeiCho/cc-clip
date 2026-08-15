package doctor

import (
	"fmt"
	"strings"

	"github.com/shunmei/cc-clip/internal/shim"
)

// Notification-bridge checks (#22 P1). Each probe prints exactly one marker
// (the tunnel-probe pattern) so classification survives shell noise, and the
// detection keys come from the shim package — the same strings connect
// installs — so doctor can never drift from the deploy.
//
// Classification philosophy, informed by real field states: a bare
// user-authored "cc-clip-hook" and a pre-managed-era codex notify line both
// WORK — they are reported OK with a migration note, not as failures. Failures
// are reserved for states where notifications demonstrably cannot fire.

// claudeHooksProbeCommand classifies how (or whether) Claude Code notification
// hooks are wired on the remote. Managed is checked before user-authored:
// both patterns can appear in one file and managed is the stronger claim.
var claudeHooksProbeCommand = fmt.Sprintf(`f="$HOME/.claude/settings.json"
if [ ! -f "$f" ]; then
    echo 'claude-hooks:no-file'
elif grep -qF '%s' "$f"; then
    echo 'claude-hooks:managed'
elif grep -qF 'cc-clip-hook' "$f"; then
    echo 'claude-hooks:user-authored'
else
    echo 'claude-hooks:none'
fi`, shim.ClaudeManagedOwnerPrefix)

// codexNotifyProbeCommand classifies the notify wiring in ~/.codex/config.toml.
var codexNotifyProbeCommand = fmt.Sprintf(`f="$HOME/.codex/config.toml"
if [ ! -f "$f" ]; then
    echo 'codex-notify:no-codex'
elif grep -qF '%s' "$f"; then
    echo 'codex-notify:managed'
elif grep -E '^[[:space:]]*("notify"|notify)[[:space:]]*=' "$f" | grep -q 'cc-clip'; then
    echo 'codex-notify:unmanaged-cc-clip'
elif grep -qE '^[[:space:]]*("notify"|notify)[[:space:]]*=' "$f"; then
    echo 'codex-notify:foreign'
else
    echo 'codex-notify:none'
fi`, shim.CodexNotifyMarkerStart)

const notifyNonceProbeCommand = `test -f "$HOME/.cache/cc-clip/notify.nonce" && echo present || echo missing`

const hookScriptProbeCommand = `test -x "$HOME/.local/bin/cc-clip-hook" && echo installed || echo missing`

func classifyClaudeHooksCheck(out string, err error) CheckResult {
	if err != nil {
		return CheckResult{"claude-hooks", false, fmt.Sprintf("remote check could not run over SSH: %v (%s)", err, strings.TrimSpace(out))}
	}
	switch {
	case strings.Contains(out, "claude-hooks:managed"):
		return CheckResult{"claude-hooks", true, "managed notify runner wired in ~/.claude/settings.json"}
	case strings.Contains(out, "claude-hooks:user-authored"):
		return CheckResult{"claude-hooks", true, "user-authored cc-clip-hook wired (works via the fallback script; delete it and re-run 'cc-clip connect <host> --claude' to migrate to the managed runner)"}
	case strings.Contains(out, "claude-hooks:no-file"):
		return CheckResult{"claude-hooks", true, "~/.claude/settings.json not found (Claude Code not set up; skipped)"}
	case strings.Contains(out, "claude-hooks:none"):
		return CheckResult{"claude-hooks", false, "Claude Code is set up but no cc-clip notification hooks are wired; run 'cc-clip connect <host> --claude'"}
	default:
		return CheckResult{"claude-hooks", false, "check did not complete (unrecognized probe output)"}
	}
}

func classifyCodexNotifyCheck(out string, err error) CheckResult {
	if err != nil {
		return CheckResult{"codex-notify", false, fmt.Sprintf("remote check could not run over SSH: %v (%s)", err, strings.TrimSpace(out))}
	}
	switch {
	case strings.Contains(out, "codex-notify:no-codex"):
		return CheckResult{"codex-notify", true, "~/.codex/config.toml not found (Codex not set up; skipped)"}
	case strings.Contains(out, "codex-notify:managed"):
		return CheckResult{"codex-notify", true, "managed notify block present in ~/.codex/config.toml"}
	case strings.Contains(out, "codex-notify:unmanaged-cc-clip"):
		return CheckResult{"codex-notify", true, "an unmanaged cc-clip notify line is configured (functional; remove it and re-run 'cc-clip connect <host> --codex' to adopt the managed block)"}
	case strings.Contains(out, "codex-notify:foreign"):
		return CheckResult{"codex-notify", true, "a non-cc-clip notify setting is configured; cc-clip respects it and will refuse to inject over it"}
	case strings.Contains(out, "codex-notify:none"):
		return CheckResult{"codex-notify", false, "Codex is set up but notifications are not wired; run 'cc-clip connect <host> --codex'"}
	default:
		return CheckResult{"codex-notify", false, "check did not complete (unrecognized probe output)"}
	}
}

func classifyNotifyNonceCheck(out string, err error) CheckResult {
	if err != nil {
		return CheckResult{"notify-nonce", false, fmt.Sprintf("remote check could not run over SSH: %v (%s)", err, strings.TrimSpace(out))}
	}
	if strings.Contains(out, "present") {
		return CheckResult{"notify-nonce", true, "notification nonce present"}
	}
	return CheckResult{"notify-nonce", false, "notification nonce missing — notifications cannot authenticate; run 'cc-clip connect <host>'"}
}

func classifyHookScriptCheck(out string, err error) CheckResult {
	if err != nil {
		return CheckResult{"notify-hook-script", false, fmt.Sprintf("remote check could not run over SSH: %v (%s)", err, strings.TrimSpace(out))}
	}
	if strings.Contains(out, "installed") {
		return CheckResult{"notify-hook-script", true, "fallback hook script installed at ~/.local/bin/cc-clip-hook"}
	}
	return CheckResult{"notify-hook-script", false, "cc-clip-hook missing from ~/.local/bin; run 'cc-clip connect <host>'"}
}
