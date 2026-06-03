package shim

import (
	"fmt"
	"strings"
)

// opencodePluginDir is the GLOBAL opencode plugin load directory (host-wide,
// matching "install once per remote"). The directory name is PLURAL `plugins`
// — verified on opencode 1.3.16; the design doc's singular `plugin` is wrong.
const opencodePluginDir = "$HOME/.config/opencode/plugins"

// opencodePluginFile is the dropped plugin filename. opencode loads any .js in
// the plugins dir, so the name is cc-clip's own and must stay stable so the
// strip helper can find and remove it.
const opencodePluginFile = "cc-clip-notify.js"

// RemoteHasOpencode reports whether the opencode CLI is runnable on the remote.
// It probes `command -v opencode` rather than a config directory: a present
// ~/.config/opencode/ directory does NOT imply the opencode executable is on
// PATH. Tri-state parsing (yes/no/garbage) mirrors RemoteHasAgy.
func RemoteHasOpencode(session RemoteExecutor) (bool, error) {
	out, err := session.Exec("if command -v opencode >/dev/null 2>&1; then echo yes; else echo no; fi")
	if err != nil {
		return false, fmt.Errorf("failed to check for remote opencode CLI: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected opencode probe output: %q", out)
	}
}

// buildOpencodePluginScript returns the bash script that drops the cc-clip
// opencode notify plugin into the global plugins dir.
//
// Crash-safety (mirrors EnsureRemoteCodexNotifyConfig): the plugin source is
// written into a mktemp file inside the plugins dir (same filesystem) and then
// mv'd over cc-clip-notify.js so the final swap is atomic via rename(2). `set -e`
// plus an EXIT trap that removes the temp file means any internal failure
// (mkdir, mktemp, write, mv) propagates to a non-zero exit code instead of being
// masked by the trailing cleanup, and never leaves a half-written plugin.
func buildOpencodePluginScript(port int) string {
	return fmt.Sprintf(`set -e
dir=%[1]s
mkdir -p "$dir"
tmp=$(mktemp "$dir/.cc-clip-notify.js.XXXXXX")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp" <<'CC_CLIP_OPENCODE_PLUGIN_EOF'
%[3]s
CC_CLIP_OPENCODE_PLUGIN_EOF
chmod 0644 "$tmp"
mv "$tmp" "$dir/%[2]s"
trap - EXIT
`, opencodePluginDir, opencodePluginFile, opencodePluginJS(port))
}

// EnsureRemoteOpencodePlugin installs (or reinstalls) the cc-clip opencode
// notify plugin on the remote by dropping cc-clip-notify.js into the global
// opencode plugins dir via an atomic mktemp+mv. Any failure (mkdir, write, mv)
// is surfaced for diagnosis.
//
// Note: a successful drop only proves the file was written, not that opencode
// loads it or that session.idle fires; the deploy state records Verified=false
// accordingly.
func EnsureRemoteOpencodePlugin(session RemoteExecutor, port int) error {
	out, err := session.Exec(buildOpencodePluginScript(port))
	if err != nil {
		return fmt.Errorf("failed to install opencode notify plugin: %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

// buildOpencodeStripScript returns the bash script that removes the dropped
// cc-clip-notify.js plugin. `rm -f` is a no-op when the file is absent.
func buildOpencodeStripScript() string {
	return fmt.Sprintf(`rm -f %s/%s
`, opencodePluginDir, opencodePluginFile)
}

// StripRemoteOpencodePlugin removes the dropped cc-clip-notify.js plugin from
// the remote opencode plugins dir; it is a no-op when the file is absent.
// Mirrors StripRemoteCodexNotifyConfig.
//
// Symmetric helper only — unit-tested, but NOT called from any uninstall branch
// in Step 7 (there is no `--opencode` uninstall branch; see the plan's "Scope:
// uninstall"). It exists so the future uninstall-split has the primitive ready.
func StripRemoteOpencodePlugin(session RemoteExecutor) error {
	if _, err := session.Exec(buildOpencodeStripScript()); err != nil {
		return fmt.Errorf("failed to strip cc-clip opencode notify plugin: %w", err)
	}
	return nil
}
