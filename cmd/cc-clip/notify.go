package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/install"
	"github.com/shunmei/cc-clip/internal/plugin"
	"github.com/shunmei/cc-clip/internal/shim"
)

// adapterOutcome captures one detect-install adapter's per-connect result.
// attempted means the adapter was targeted this connect (so its existing
// deploy-state entry may be refreshed/downgraded); installed means the notify
// integration was successfully written.
type adapterOutcome struct {
	attempted bool
	installed bool
}

// detectInstallAdapter describes a notify adapter that follows the uniform
// "binary-detected, then configured" shape: gate on a target predicate, probe
// the remote for the agent, and install its notify integration if present.
//
// Claude is intentionally NOT modeled here — its settings.json merge, opt-out
// marker, and wrapper fallback do not generalize (see configureRemoteClaudeHooks).
// This table IS the extension seam for future notify CLIs (copilot, cursor, ...):
// add a row with its predicate, detector, and installer; the deploy-state
// recording is then one more applyAdapterState call in mergeNotifyDeployState.
type detectInstallAdapter struct {
	id       shim.AdapterID
	label    string                                  // human label, e.g. "Codex", "Antigravity"
	step     string                                  // step tag for the progress line, e.g. "N5", "N5.5"
	fileNote string                                  // success line printed after install
	targeted func(DeployTargets) bool                // gate predicate (5.3c: untargeted => skipped, config untouched)
	detect   func(shim.RemoteExecutor) (bool, error) // remote presence probe
	install  func(shim.RemoteExecutor, int) error    // notify integration writer
}

// run executes one adapter's detect-install flow and returns its outcome. A
// non-targeted adapter is skipped entirely (attempted=false) so 5.3c gating
// preserves its existing deploy-state entry. When targeted, attempted=true
// regardless of detection so a stale installed entry can be downgraded if the
// agent is no longer present or install fails.
func (a detectInstallAdapter) run(session shim.RemoteExecutor, port int, targets DeployTargets) adapterOutcome {
	if !a.targeted(targets) {
		fmt.Printf("  [%s] Skipping %s notify (%s not targeted)\n", a.step, a.label, a.label)
		return adapterOutcome{}
	}
	has, err := a.detect(session)
	if err != nil {
		log.Printf("      warning: %s detection failed: %v", a.label, err)
		return adapterOutcome{attempted: true}
	}
	if !has {
		fmt.Printf("  [%s] %s not detected, skipping notify setup\n", a.step, a.label)
		return adapterOutcome{attempted: true}
	}
	fmt.Printf("  [%s] %s detected, configuring notify integration...\n", a.step, a.label)
	return installOutcome(a.label, a.fileNote, a.install(session, port))
}

// installOutcome turns an adapter installer's result into the progress line and
// outcome. A cc-clip notify line that already sits outside the managed block
// works (doctor reports it as a pass), so it counts as wired with an adopt
// hint rather than as a failure; any other error stays a warning.
func installOutcome(label, fileNote string, err error) adapterOutcome {
	var wired *shim.UnmanagedNotifyError
	switch {
	case err == nil:
		fmt.Printf("      %s\n", fileNote)
		return adapterOutcome{attempted: true, installed: true}
	case errors.As(err, &wired):
		fmt.Printf("      %s\n", wired.Note)
		return adapterOutcome{attempted: true, installed: true}
	default:
		log.Printf("      warning: %s notify setup failed: %v", label, err)
		return adapterOutcome{attempted: true}
	}
}

// buildNotifyAdapters returns the ordered detect-install notify adapter table.
// Extracted from connectNotifySetup so tests can assert the registered adapters
// (ids/predicates) without reaching into connect's runtime. Each row is gated on
// its target predicate (5.3c: an untargeted adapter is skipped and its config
// file is never written). Adding a future notify CLI = one more row here plus its
// shim detector/installer.
func buildNotifyAdapters() []detectInstallAdapter {
	return []detectInstallAdapter{
		{
			id:       shim.AdapterCodexNotify,
			label:    "Codex",
			step:     "N5",
			fileNote: "~/.codex/config.toml updated",
			targeted: codexTargeted,
			detect:   shim.RemoteHasCodex,
			install:  shim.EnsureRemoteCodexNotifyConfig,
		},
		{
			id:       shim.AdapterAntigravityNotify,
			label:    "Antigravity",
			step:     "N5.5",
			fileNote: "cc-clip-notify agy plugin installed",
			targeted: agyTargeted,
			detect:   shim.RemoteHasAgy,
			install:  shim.EnsureRemoteAntigravityPlugin,
		},
		{
			id:       shim.AdapterOpencodeNotify,
			label:    "opencode",
			step:     "N5.7",
			fileNote: "cc-clip opencode notify plugin installed",
			targeted: opencodeNotifyTargeted,
			detect:   shim.RemoteHasOpencode,
			install:  shim.EnsureRemoteOpencodePlugin,
		},
		{
			id:       shim.AdapterCursorNotify,
			label:    "Cursor",
			step:     "N5.9",
			fileNote: "~/.cursor/hooks.json stop hook merged",
			targeted: cursorTargeted,
			detect:   shim.RemoteHasCursor,
			install:  shim.EnsureRemoteCursorHooks,
		},
	}
}

// runDetectInstallAdapters runs each detect-install adapter and collects the
// per-adapter outcomes keyed by adapter id.
func runDetectInstallAdapters(session shim.RemoteExecutor, port int, targets DeployTargets, adapters []detectInstallAdapter) map[shim.AdapterID]adapterOutcome {
	outcomes := make(map[shim.AdapterID]adapterOutcome, len(adapters))
	for _, a := range adapters {
		outcomes[a.id] = a.run(session, port, targets)
	}
	return outcomes
}

// connectNotifySetup performs notification bridge setup:
// 1. Generate nonce and register with local daemon
// 2. Write nonce to remote
// 3. Install hook script on remote
// 4. Configure Claude Code hooks (if Claude targeted)
// 5/5.5. Detect-install notify adapters (Codex, Antigravity, opencode) via the
//
//	detectInstallAdapter table, each gated on its target
//
// 6. Run health probe
func connectNotifySetup(session *shim.SSHSession, port int, daemonToken, host string, state *shim.DeployState, opts connectOpts) {
	fmt.Println()
	fmt.Println("Notification bridge setup:")

	// Step N1: Generate nonce
	fmt.Println("  [N1] Generating notification nonce...")
	notifyNonce, err := syncNotificationNonce(session, port, daemonToken, host)
	if err != nil {
		if errors.Is(err, errNotifyDisabled) {
			fmt.Println("      skipped: the local daemon has notifications off (serve --features without notify)")
			removeNotifyHooks(session, state, opts)
			return
		}
		log.Printf("      warning: failed to sync notification nonce: %v", err)
		// --no-hooks is a removal request and must not depend on the nonce.
		removeClaudeHooksIfRequested(session, port, opts)
		return
	}
	fmt.Println("      nonce synced")

	// Step N3: Install hook script
	fmt.Println("  [N3] Installing hook script...")
	if err := shim.InstallRemoteHookScript(session, port); err != nil {
		log.Printf("      warning: failed to install hook script: %v", err)
		return
	}
	fmt.Println("      cc-clip-hook installed to ~/.local/bin/cc-clip-hook")

	hookInstalled := true

	// Step N4: Install Claude Code hooks in durable settings.json first — only
	// when Claude is targeted. The wrapper remains only as a fallback for
	// settings merge failures. 5.3c: --codex/--opencode/--agy must NOT write
	// ~/.claude/settings.json, so an untargeted run skips N4 entirely and
	// preserves any existing claude-notify adapter (applyAdapterState).
	var claudeHooks claudeHooksResult
	if claudeTargeted(opts.targets) {
		fmt.Println("  [N4] Configuring Claude Code hooks...")
		claudeHooks = configureRemoteClaudeHooks(session, port, opts)
	} else {
		fmt.Println("  [N4] Skipping Claude hooks (Claude not targeted)")
	}

	// Steps N5/N5.5/N5.7: detect-install notify adapters. Each row is gated on
	// its target predicate; 5.3c is preserved — an untargeted adapter is skipped
	// entirely (its config file is never written) and its existing deploy-state
	// entry is left untouched. Adding a future notify CLI = adding a row to
	// buildNotifyAdapters() plus its shim detector/installer.
	notifyAdapters := buildNotifyAdapters()
	adapterOutcomes := runDetectInstallAdapters(session, port, opts.targets, notifyAdapters)

	// Step N6: Health probe
	fmt.Println("  [N6] Running notification health probe...")
	healthVerified := false
	if err := runNotificationHealthProbe(port, notifyNonce); err != nil {
		log.Printf("      warning: health probe failed: %v", err)
	} else {
		healthVerified = true
		fmt.Println("      health probe passed")
	}

	// Update deploy state: refresh the legacy boolean fields and record per-adapter
	// truth. Each adapter is marked installed ONLY when genuinely wired this connect
	// (claude-notify: managed runner in settings.json; codex-notify: config.toml
	// injected) — not merely because the cc-clip-hook script exists. Both adapters
	// are attempted on every pre-Step-5 connect (N4 Claude, N5 Codex), so a stale
	// installed entry is downgraded when wiring did not succeed; Step 5 gates
	// attempted on DeployTargets so un-targeted adapters are preserved instead.
	codexOut := adapterOutcomes[shim.AdapterCodexNotify]
	agyOut := adapterOutcomes[shim.AdapterAntigravityNotify]
	opencodeOut := adapterOutcomes[shim.AdapterOpencodeNotify]
	cursorOut := adapterOutcomes[shim.AdapterCursorNotify]
	mergeNotifyDeployState(state, notifyOutcome{
		hookScriptInstalled: hookInstalled,
		claudeAttempted:     claudeTargeted(opts.targets),
		claudeWired:         claudeHooks.adapterInstalled,
		codexAttempted:      codexOut.attempted,
		codexInjected:       codexOut.installed,
		agyAttempted:        agyOut.attempted,
		agyInstalled:        agyOut.installed,
		opencodeAttempted:   opencodeOut.attempted,
		opencodeInstalled:   opencodeOut.installed,
		cursorAttempted:     cursorOut.attempted,
		cursorInstalled:     cursorOut.installed,
		healthVerified:      healthVerified,
	})
}

// notifyOutcome captures what this connect attempted and achieved for each notify
// adapter so mergeNotifyDeployState can record per-adapter truth without
// over-claiming. "attempted" means this connect targeted the adapter — pre-Step-5
// both are always attempted (N4 configures Claude hooks, N5 probes Codex); Step 5
// gates them on DeployTargets. When an adapter is NOT attempted, its existing
// deploy-state entry is preserved untouched.
type notifyOutcome struct {
	hookScriptInstalled bool // cc-clip-hook fallback script installed on disk (N3)
	claudeAttempted     bool // this connect configured Claude hooks (N4)
	claudeWired         bool // managed runner genuinely wired in ~/.claude/settings.json
	codexAttempted      bool // this connect probed/attempted Codex notify (N5)
	codexInjected       bool // ~/.codex/config.toml notify successfully injected
	agyAttempted        bool // this connect probed/attempted Antigravity notify (N5.5)
	agyInstalled        bool // cc-clip-notify agy plugin installed via the agy CLI
	opencodeAttempted   bool // this connect probed/attempted opencode notify (N5.7)
	opencodeInstalled   bool // cc-clip-notify.js opencode plugin dropped into the plugins dir
	cursorAttempted     bool // this connect probed/attempted Cursor notify (N5.9)
	cursorInstalled     bool // the managed stop hook is present in ~/.cursor/hooks.json
	healthVerified      bool // N6 health probe passed
}

// mergeNotifyDeployState refreshes the legacy boolean fields on state.Notify and
// records per-adapter truth for this connect via applyAdapterState. The legacy
// HookInstalled reflects the cc-clip-hook fallback SCRIPT on disk; the adapter
// entries reflect ACTUAL wiring, decoupled from script presence so the state
// never over-claims (P3). CodexInjected is refreshed ONLY when Codex was
// attempted, so the legacy field never contradicts a preserved adapter entry.
func mergeNotifyDeployState(state *shim.DeployState, o notifyOutcome) {
	if state.Notify == nil {
		state.Notify = &shim.NotifyDeployState{}
	}
	state.Notify.Enabled = true
	state.Notify.HookInstalled = o.hookScriptInstalled
	state.Notify.HealthVerified = o.healthVerified
	if o.codexAttempted {
		state.Notify.CodexInjected = o.codexInjected
	}

	applyAdapterState(state.Notify, shim.AdapterClaudeNotify, o.claudeAttempted, o.claudeWired)
	applyAdapterState(state.Notify, shim.AdapterCodexNotify, o.codexAttempted, o.codexInjected)
	// agy-notify has no legacy boolean mirror; it is tracked purely via the
	// per-adapter map. Verified stays false (applyAdapterState) because a
	// successful `agy plugin install` proves only that the layout was accepted,
	// not that the Stop hook fires.
	applyAdapterState(state.Notify, shim.AdapterAntigravityNotify, o.agyAttempted, o.agyInstalled)
	// opencode-notify likewise has no legacy boolean mirror. Verified stays false
	// because a successful plugin drop proves only the file landed, not that
	// opencode loads it or that session.idle fires.
	applyAdapterState(state.Notify, shim.AdapterOpencodeNotify, o.opencodeAttempted, o.opencodeInstalled)
	// cursor-notify likewise has no legacy boolean mirror. Verified stays false
	// because a successful merge proves only that hooks.json now names our
	// command — not that Cursor loads the file, nor that its stop event fires
	// in the CLI, which is the part that has historically been unreliable.
	applyAdapterState(state.Notify, shim.AdapterCursorNotify, o.cursorAttempted, o.cursorInstalled)
}

// applyAdapterState records one adapter's per-connect truth without over-claiming:
//   - !attempted          -> preserve any existing entry untouched (not targeted
//     this connect; Step 5 gates attempted on DeployTargets).
//   - attempted && wired  -> Installed=true, Source=config, Verified=false (the N6
//     probe re-verifies the runner path next connect).
//   - attempted && !wired -> downgrade a stale installed entry to Installed=false
//     (attempted this connect but not wired); absence already means "not
//     installed", so no entry is created.
func applyAdapterState(notify *shim.NotifyDeployState, id shim.AdapterID, attempted, wired bool) {
	if !attempted {
		return
	}
	if wired {
		if notify.Adapters == nil {
			notify.Adapters = make(map[shim.AdapterID]*shim.AdapterState)
		}
		notify.Adapters[id] = &shim.AdapterState{
			Installed: true, Source: install.SourceConfig, Verified: false,
		}
		return
	}
	if existing, ok := notify.Adapters[id]; ok {
		existing.Installed = false
		existing.Verified = false
	}
}

// claudeHooksResult reports what configureRemoteClaudeHooks actually achieved so
// the deploy state records the claude-notify adapter as installed ONLY when the
// managed plugin runner is genuinely wired into ~/.claude/settings.json — not
// merely because the cc-clip-hook fallback script exists on disk.
type claudeHooksResult struct {
	// adapterInstalled is true ONLY when the managed runner is present in
	// settings.json after this call (freshly merged OR already present, with no
	// per-event skip). It is false for: --no-hooks, the persistent opt-out
	// marker, a user-bare hook that suppressed insertion (any merge warning), and
	// the wrapper/manual fallback (the wrapper is not the plugin-runner adapter).
	adapterInstalled bool
	// usedFallback is true when settings merge failed and the legacy wrapper (or
	// manual config) path was taken instead of the durable settings runner.
	usedFallback bool
}

// removeClaudeHooksIfRequested honours --no-hooks on paths that skip the rest
// of the notification setup (--no-notify, or a daemon with notifications off).
func removeClaudeHooksIfRequested(session shim.SessionExecutor, port int, opts connectOpts) {
	if !opts.noHooks || !claudeTargeted(opts.targets) {
		return
	}
	fmt.Println("  [N4] Removing Claude hooks (--no-hooks)...")
	configureRemoteClaudeHooks(session, port, opts)
}

// removeNotifyHooks handles a local daemon whose --features leaves out notify.
// Its nonce can never register, so every agent hook wired by an earlier connect
// would only start a process per turn whose POST is refused. The managed
// Claude, Cursor, Codex and opencode integrations of the targeted agents are
// removed without writing the no-hooks marker, so adding notify back and
// re-running connect restores them; an explicit --no-hooks still writes it.
// The Antigravity plugin has no removal helper yet and is left in place.
// The deploy state stops claiming an integration only once it is removed.
func removeNotifyHooks(session shim.SessionExecutor, state *shim.DeployState, opts connectOpts) {
	removed := map[shim.AdapterID]bool{}
	if claudeTargeted(opts.targets) {
		fmt.Println("  [N4] Removing Claude hooks (notifications are off)...")
		if opts.noHooks {
			configureRemoteClaudeHooks(session, 0, opts)
			removed[shim.AdapterClaudeNotify] = true
		} else {
			removed[shim.AdapterClaudeNotify] = removeClaudeManagedHooks(session)
		}
	}
	if cursorTargeted(opts.targets) {
		if changed, err := shim.RemoveRemoteCursorManagedHooks(session); err != nil {
			log.Printf("      warning: failed to remove managed Cursor hooks: %v", err)
		} else {
			removed[shim.AdapterCursorNotify] = true
			if changed {
				fmt.Println("      managed stop hook removed from ~/.cursor/hooks.json")
			}
		}
	}
	if codexTargeted(opts.targets) {
		if err := shim.StripRemoteCodexNotifyConfig(session); err != nil {
			log.Printf("      warning: failed to remove the Codex notify config: %v", err)
		} else {
			removed[shim.AdapterCodexNotify] = true
		}
	}
	if opencodeNotifyTargeted(opts.targets) {
		if err := shim.StripRemoteOpencodePlugin(session); err != nil {
			log.Printf("      warning: failed to remove the opencode notify plugin: %v", err)
		} else {
			removed[shim.AdapterOpencodeNotify] = true
		}
	}

	if state == nil || state.Notify == nil {
		return
	}
	state.Notify.Enabled = false
	state.Notify.HealthVerified = false
	for id, ok := range removed {
		if !ok {
			continue
		}
		applyAdapterState(state.Notify, id, true, false)
		if id == shim.AdapterCodexNotify {
			state.Notify.CodexInjected = false
		}
	}
}

// removeClaudeManagedHooks removes cc-clip's managed hooks from
// ~/.claude/settings.json and the legacy claude wrapper, and reports whether
// both steps succeeded. User-authored hooks are left alone.
func removeClaudeManagedHooks(session shim.SessionExecutor) bool {
	ok := true
	if changed, err := shim.RemoveRemoteClaudeManagedHooks(session); err != nil {
		log.Printf("      warning: failed to remove managed Claude settings hooks: %v", err)
		ok = false
	} else if changed {
		fmt.Println("      managed hooks removed from ~/.claude/settings.json")
	}
	if removed, err := shim.UninstallRemoteClaudeWrapperIfPresent(session); err != nil {
		log.Printf("      warning: failed to remove legacy claude wrapper: %v", err)
		ok = false
	} else if removed {
		fmt.Println("      legacy claude wrapper removed; original entry restored")
	}
	return ok
}

func configureRemoteClaudeHooks(session shim.SessionExecutor, port int, opts connectOpts) claudeHooksResult {
	if opts.noHooks {
		removeClaudeManagedHooks(session)
		if err := shim.SetRemoteClaudeHooksEnabled(session, false); err != nil {
			log.Printf("      warning: failed to disable Claude hooks: %v", err)
		} else {
			fmt.Println("      Claude hook injection disabled by ~/.cache/cc-clip/no-hooks")
		}
		return claudeHooksResult{}
	}

	if opts.hooks {
		if err := shim.SetRemoteClaudeHooksEnabled(session, true); err != nil {
			log.Printf("      warning: failed to re-enable Claude hooks: %v", err)
		}
	} else {
		disabled, err := shim.RemoteClaudeHooksDisabled(session)
		if err != nil {
			log.Printf("      warning: failed to check Claude hook opt-out marker: %v", err)
		} else if disabled {
			removeClaudeManagedHooks(session)
			fmt.Println("      Claude hook injection disabled by ~/.cache/cc-clip/no-hooks")
			return claudeHooksResult{}
		}
	}

	changed, warnings, err := shim.MergeRemoteClaudeSettingsHooks(session, port)
	if err == nil {
		for _, w := range warnings {
			log.Printf("      warning: %s", w)
		}
		if changed {
			fmt.Println("      hooks installed in ~/.claude/settings.json")
		} else {
			fmt.Println("      hooks already present in ~/.claude/settings.json")
		}
		if removed, err := shim.UninstallRemoteClaudeWrapperIfPresent(session); err != nil {
			log.Printf("      warning: failed to remove legacy claude wrapper: %v", err)
		} else if removed {
			fmt.Println("      legacy claude wrapper removed; original entry restored")
		}
		return claudeHooksResult{adapterInstalled: len(warnings) == 0}
	}

	log.Printf("      warning: failed to merge ~/.claude/settings.json hooks: %v", err)
	fmt.Println("      Falling back to legacy claude wrapper (may be overwritten by Claude Code self-update)")
	if err := shim.InstallRemoteClaudeWrapper(session, port); err != nil {
		log.Printf("      warning: failed to install claude wrapper: %v", err)
		fmt.Println("      Falling back to manual hook config:")
		fmt.Println()
		for _, line := range strings.Split(claudeHookConfigJSON(), "\n") {
			fmt.Printf("      %s\n", line)
		}
		fmt.Println()
	} else {
		fmt.Println("      claude wrapper installed to ~/.local/bin/claude")
	}
	return claudeHooksResult{usedFallback: true}
}

func syncNotificationNonce(session *shim.SSHSession, port int, daemonToken, host string) (string, error) {
	notifyNonce, err := shim.GenerateNotificationNonce()
	if err != nil {
		return "", fmt.Errorf("failed to generate notification nonce: %w", err)
	}
	if err := registerNonceWithDaemon(port, daemonToken, notifyNonce, host); err != nil {
		return "", fmt.Errorf("failed to register nonce with daemon: %w", err)
	}
	if err := shim.WriteRemoteNotificationNonce(session, notifyNonce); err != nil {
		return "", fmt.Errorf("failed to write remote nonce: %w", err)
	}
	return notifyNonce, nil
}

// registerNonceWithDaemon sends the notification nonce to the local daemon
// via POST /register-nonce, authenticated with the clipboard bearer token.
// host binds the nonce to the SSH target so the daemon can revoke any
// previously issued nonce for the same host on reconnect.
func registerNonceWithDaemon(port int, bearerToken, nonce, host string) error {
	payloadBytes, err := json.Marshal(struct {
		Nonce string `json:"nonce"`
		Host  string `json:"host,omitempty"`
	}{Nonce: nonce, Host: host})
	if err != nil {
		return fmt.Errorf("failed to encode register-nonce payload: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/register-nonce", port)

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cc-clip/connect")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get(daemon.DisabledFeatureHeader) == "notify" {
		return errNotifyDisabled
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	return nil
}

// errNotifyDisabled means the local daemon's --features leaves out notify.
var errNotifyDisabled = errors.New("daemon has notifications disabled")

// runNotificationHealthProbe sends a test notification to the local daemon
// via /notify and checks for 204. This proves the nonce is registered and
// the notification pipeline works end-to-end.
func runNotificationHealthProbe(port int, nonce string) error {
	payload := `{"title":"cc-clip","body":"Notification bridge connected","urgency":0}`
	url := fmt.Sprintf("http://127.0.0.1:%d/notify", port)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+nonce)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cc-clip-hook/0.1")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health probe request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("expected 204, got %d", resp.StatusCode)
	}
	return nil
}

func claudeHookConfigJSON() string {
	return `{
  "hooks": {
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-clip-hook"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-clip-hook"
          }
        ]
      }
    ]
  }
}`
}

// --- Notify subcommand ---

// cmdNotify sends a generic notification to the local cc-clip daemon.
func cmdNotify() {
	fs, f := newNotifyFlagSet(flag.ExitOnError, getPort())
	_ = fs.Parse(os.Args[2:])

	msg := daemon.GenericMessagePayload{
		Title:    *f.title,
		Body:     *f.body,
		Urgency:  *f.urgency,
		Sound:    *f.sound,
		Verified: *f.trusted,
	}

	switch {
	case *f.fromCodex != "" && *f.fromCodexStdin:
		log.Fatal("notify failed: --from-codex and --from-codex-stdin are mutually exclusive")
	case *f.fromCodex != "":
		parsed, err := parseCodexNotifyPayload(*f.fromCodex)
		if err != nil {
			log.Fatalf("invalid codex notify payload: %v", err)
		}
		msg = parsed
	case *f.fromCodexStdin:
		payload, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("failed to read codex payload from stdin: %v", err)
		}
		parsed, err := parseCodexNotifyPayload(string(payload))
		if err != nil {
			log.Fatalf("invalid codex notify payload: %v", err)
		}
		msg = parsed
	}

	if *f.sound != "" {
		msg.Sound = *f.sound
	}
	if *f.trusted {
		msg.Verified = true
	}

	// The --from-codex forms are Codex's own notify contract, so they attribute
	// the delivery receipt; a hand-run notification stays unattributed.
	target := ""
	if *f.fromCodex != "" || *f.fromCodexStdin {
		target = "codex"
	}

	if err := postGenericNotification(*f.port, target, msg); err != nil {
		log.Fatalf("notify failed: %v", err)
	}
}

// notifyFlags holds the parsed values of `cc-clip notify`.
type notifyFlags struct {
	title, body, sound, fromCodex *string
	urgency, port                 *int
	trusted, fromCodexStdin       *bool
}

// newNotifyFlagSet defines notify's flags. --port defaults to defaultPort
// (CC_CLIP_PORT or 18339) so an explicit flag wins and a non-numeric value is
// rejected instead of ignored.
func newNotifyFlagSet(errorHandling flag.ErrorHandling, defaultPort int) (*flag.FlagSet, notifyFlags) {
	fs := flag.NewFlagSet("notify", errorHandling)
	f := notifyFlags{
		title:          fs.String("title", "", "notification title"),
		body:           fs.String("body", "", "notification body"),
		urgency:        fs.Int("urgency", 1, "notification urgency (0=low, 1=normal, 2=critical)"),
		sound:          fs.String("sound", "", "macOS notification sound"),
		trusted:        fs.Bool("trusted", false, "mark this notification as trusted"),
		fromCodex:      fs.String("from-codex", "", "Codex notify JSON payload"),
		fromCodexStdin: fs.Bool("from-codex-stdin", false, "read Codex notify JSON payload from stdin"),
		port:           fs.Int("port", defaultPort, "daemon port (env: CC_CLIP_PORT)"),
	}
	return fs, f
}

// parseCodexNotifyPayload extracts a GenericMessagePayload from the Codex
// JSON format. Codex passes {"last-assistant-message": "..."} as its notify
// payload. The extracted message becomes the body with title "Codex".
func parseCodexNotifyPayload(payload string) (daemon.GenericMessagePayload, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return daemon.GenericMessagePayload{}, fmt.Errorf("failed to parse JSON: %w", err)
	}

	lastMsg, _ := raw["last-assistant-message"].(string)

	return daemon.GenericMessagePayload{
		Title:    "Codex",
		Body:     lastMsg,
		Urgency:  1,
		Verified: true,
	}, nil
}

// postGenericNotification sends a generic notification to the local cc-clip daemon.
// It delegates to the shared plugin.PostNotification core so the wire bytes stay
// identical across the notify subcommand and the plugin runner.
func postGenericNotification(port int, target string, msg daemon.GenericMessagePayload) error {
	return plugin.PostNotification(port, target, msg)
}
