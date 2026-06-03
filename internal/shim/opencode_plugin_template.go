package shim

import "fmt"

// opencodePluginJS returns the opencode plugin source dropped on the remote at
// ~/.config/opencode/plugins/cc-clip-notify.js. It forwards opencode
// session.idle (turn-complete) events to the local machine via the cc-clip
// notify tunnel by shelling out to `cc-clip plugin run opencode-notify`.
//
// The port is baked into the shell command at GENERATION time (Go), not at JS
// runtime: the default port 18339 emits the bare command, while a non-default
// port prepends `env CC_CLIP_PORT=<port>` so the runner reaches the right
// daemon. This mirrors antigravityHookCommand's port branch — the JS never
// interpolates the port itself.
func opencodePluginJS(port int) string {
	cmd := "cc-clip plugin run opencode-notify"
	if port != 18339 {
		cmd = fmt.Sprintf("env CC_CLIP_PORT=%d %s", port, cmd)
	}
	return fmt.Sprintf(`// cc-clip-notify.js — installed by `+"`cc-clip connect <host> --opencode`"+`.
// Forwards opencode session.idle (turn-complete) events to the local machine
// via the cc-clip notify tunnel. Fire-and-forget: never throws, never blocks.
export const CcClipNotifyPlugin = async ({ $ }) => ({
  event: async ({ event }) => {
    // Only session.idle is a verified opencode event type. Permission/error
    // events are intentionally not handled yet (their type strings are still
    // shifting across opencode versions).
    if (event.type !== "session.idle") return
    try {
      // BunShell subprocess: pipe the JSON envelope on stdin so the Go runner
      // parses it exactly like codex/agy. .nothrow()/.quiet() keep it silent.
      const proc = $`+"`%s`"+`.quiet().nothrow()
      const writer = proc.stdin.getWriter()
      await writer.write(new TextEncoder().encode(JSON.stringify({ event })))
      await writer.close()
      await proc
    } catch (_) {
      /* fire-and-forget: a notify failure must never disrupt opencode */
    }
  },
})
`, cmd)
}
