# Step 7: opencode notify plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Execute in a FRESH context (this plan exists because Step 7 is a multi-file feature and should not be implemented in a strained/expensive context).

**Goal:** Auto-install an opencode plugin during `cc-clip connect <host> --opencode` (and `--all`) that forwards opencode `session.idle` (turn-complete) events to the local machine as native notifications via the existing cc-clip notify tunnel. Clipboard paste for opencode already works via the xclip/wl-paste shim — this step is **notify-only**.

**Architecture:** Mirror the codex/agy notify adapters. A JS plugin dropped on the remote shells out (BunShell `$`) to `cc-clip plugin run opencode-notify`, piping a JSON event envelope on stdin; the Go runner parses it into a `daemon.GenericMessagePayload` and POSTs it to the daemon `/notify` endpoint — the exact path codex/agy use (no new daemon envelope kind).

**Tech Stack:** Go (cc-clip), JavaScript ES module (opencode plugin, `@opencode-ai/plugin` API).

---

## Verified facts (empirically confirmed on opencode 1.3.16 — do NOT re-litigate)

| Fact | Value | Note |
|------|-------|------|
| Plugin load dir (global) | `~/.config/opencode/plugins/` (PLURAL `plugins`) | Verified: real plugins `claude-mem.js`, `caveman/` live there. Design doc's `~/.opencode/plugins/` and singular `plugin` are WRONG. |
| Plugin load dir (project) | `.opencode/plugins/` | cc-clip uses the GLOBAL dir (host-wide, matches "install once per remote"). |
| Install mechanism | Drop a `.js` file into the plugins dir | NOT `opencode plugin <module>` (that is for npm packages). NOT a config-file edit. |
| Plugin form | `export const X = async ({ $ }) => ({ event: async ({ event }) => {...} })` | Official `event` hook. Do NOT copy local plugins' compat forms. |
| Supported event (this step) | `event.type === "session.idle"` | Confirmed by official docs + `@opencode-ai/sdk` types + local install. |
| Permission/error events | DEFERRED | Names still shifting (`permission.asked`/`permission.replied` vs older types). Do NOT hardcode now. |
| `$` shell | `BunShell` with `.quiet()`, `.nothrow()`, `stdin` | Use subprocess stdin to pipe JSON; do NOT build JSON via shell string. |
| cc-clip seam | Fully mapped | `AdapterOpencodeNotify="opencode-notify"` already in `deploy.go:32`; runner switch `plugin.go:20-31`; parser pattern `notify.go:186`; detect-install table `main.go:~1183`; gate predicates `targets.go`. |

**Sources:** opencode.ai/docs/plugins, dev.opencode.ai/docs/cli, `@opencode-ai/plugin@1.3.16` types, local install probe (user-verified).

---

## File structure

- Create: `internal/shim/opencode.go` — `RemoteHasOpencode` detector + `EnsureRemoteOpencodePlugin` installer + `StripRemoteOpencodePlugin` helper (symmetric with `StripRemoteCodexNotifyConfig`; unit-tested but **NOT wired into any uninstall branch in Step 7** — see "Scope: uninstall" below + backlog).
- Create: `internal/shim/opencode_plugin_template.go` — `opencodePluginJS(port)` returns the JS plugin source with the port-aware command baked in.
- Create: `internal/shim/opencode_test.go` — detector tri-state + installer script assertions + JS template assertions.
- Modify: `internal/plugin/plugin.go` — add `AdapterOpencodeNotify` const + `Run` switch case.
- Modify: `internal/plugin/notify.go` — add `runOpencodeNotify` + `parseOpencodeNotifyPayload` + `opencodeBody`.
- Modify: `internal/plugin/plugin_test.go` + `notify_test.go` — runner dispatch + parser tests.
- Modify: `cmd/cc-clip/targets.go` — add `opencodeNotifyTargeted`.
- Modify: `cmd/cc-clip/main.go` — extract the `notifyAdapters` slice into a package-level `buildNotifyAdapters()` seam (see "Test seam" below), add the opencode row to it; add `notifyOutcome` opencode fields + one `applyAdapterState` line; tailor `connectSuccessSummary` if desired. **Do NOT add a `--opencode` uninstall branch** — `cmdUninstall` (main.go:420-483) has only the generic-shim path + the `--codex` special path (`cmdUninstallCodexRemote`, main.go:485); inventing a new branch is out of Step 7 scope (see "Scope: uninstall").
- Modify: `cmd/cc-clip/notify_targets_test.go` — gating tests (opencode populated under `--opencode`, preserved under `--claude`).
- Modify: README (en/zh/ja) — flip the opencode Notifications cell from "⚠️ not auto-configured" to auto-configured via `--opencode`/`--all`; design doc Step 7 row → done.

---

## The opencode plugin artifact (final form)

`opencodePluginJS(port)` generates this (port baked into the command — default 18339 → no env prefix; else `env CC_CLIP_PORT=<port>` prefix, mirroring `antigravityHookCommand`):

```javascript
// cc-clip-notify.js — installed by `cc-clip connect <host> --opencode`.
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
      const proc = $`cc-clip plugin run opencode-notify`.quiet().nothrow()
      const writer = proc.stdin.getWriter()
      await writer.write(new TextEncoder().encode(JSON.stringify({ event })))
      await writer.close()
      await proc
    } catch (_) {
      /* fire-and-forget: a notify failure must never disrupt opencode */
    }
  },
})
```

For a non-default port the shell call becomes `$\`env CC_CLIP_PORT=${port} cc-clip plugin run opencode-notify\`` — Go bakes the literal port in via the template (do NOT interpolate at JS runtime).

---

## Go side (mirror codex/agy exactly)

`internal/plugin/plugin.go`:
```go
const (
    AdapterClaudeNotify      = "claude-notify"
    AdapterCodexNotify       = "codex-notify"
    AdapterAntigravityNotify = "agy-notify"
    AdapterOpencodeNotify    = "opencode-notify" // MUST equal shim.AdapterOpencodeNotify
)
// in Run():
case AdapterOpencodeNotify:
    return runOpencodeNotify(port, stdin)
```

`internal/plugin/notify.go`:
```go
func runOpencodeNotify(port int, stdin io.Reader) error {
    b, err := io.ReadAll(stdin)
    if err != nil {
        return nil // fail-soft
    }
    parsed, perr := parseOpencodeNotifyPayload(string(b))
    if perr != nil {
        return nil // fail-soft: invalid payload must not block opencode
    }
    _ = PostNotification(port, parsed)
    return nil
}

// parseOpencodeNotifyPayload maps the JS plugin's {"event": {...}} envelope to
// a GenericMessagePayload. The JS sends JSON.stringify({ event }), so the
// opencode event object is nested under "event".
func parseOpencodeNotifyPayload(payload string) (daemon.GenericMessagePayload, error) {
    var raw struct {
        Event struct {
            Type       string                 `json:"type"`
            Properties map[string]interface{} `json:"properties"`
        } `json:"event"`
    }
    if err := json.Unmarshal([]byte(payload), &raw); err != nil {
        return daemon.GenericMessagePayload{}, fmt.Errorf("failed to parse JSON: %w", err)
    }
    return daemon.GenericMessagePayload{
        Title:    "opencode",
        Body:     opencodeBody(raw.Event.Type),
        Urgency:  1,
        Verified: true, // trusted notification (the daemon Trusted flag), as codex/agy
    }, nil
}

func opencodeBody(eventType string) string {
    switch eventType {
    case "session.idle":
        return "Session idle — awaiting input"
    default:
        return eventType
    }
}
```

`internal/shim/opencode.go` (detector + installer + uninstaller):
```go
const opencodePluginDir = "$HOME/.config/opencode/plugins"
const opencodePluginFile = "cc-clip-notify.js"

func RemoteHasOpencode(session RemoteExecutor) (bool, error) {
    // command -v opencode (executable), tri-state yes/no/garbage, like RemoteHasAgy.
}

func EnsureRemoteOpencodePlugin(session RemoteExecutor, port int) error {
    // mkdir -p the plugins dir; mktemp + write opencodePluginJS(port) + atomic mv,
    // same crash-safety as EnsureRemoteCodexNotifyConfig (set -e, trap rm tmp).
}

func StripRemoteOpencodePlugin(session RemoteExecutor) error {
    // rm -f the dropped file; no-op if absent. Mirror StripRemoteCodexNotifyConfig.
    // Symmetric helper only — unit-tested, but NOT called from any uninstall
    // branch in Step 7 (see "Scope: uninstall").
}
```

### Scope: uninstall (review P1)

The original plan said "wire `StripRemoteOpencodePlugin` into the `--opencode` uninstall
branch." Verified against source: **that branch does not exist.** `cmdUninstall`
(main.go:420-483) has exactly two teardown paths — the generic shim uninstall and the
`--codex` special path (`cmdUninstallCodexRemote`, main.go:485). There is no
target-aware uninstall split. Adding a `--opencode` uninstall branch would mean inventing
new uninstall semantics, which is a separate feature, not Step 7.

**Decision:** ship `StripRemoteOpencodePlugin` as a symmetric, unit-tested helper (so the
future uninstall-split has the primitive ready), but do NOT call it from connect or
uninstall in Step 7. The uninstall wiring is in the backlog below.

### Test seam (review P2)

`notifyAdapters` is currently a local variable inside `connectNotifySetup`
(main.go:1183), so there is no function boundary to assert "the opencode row exists"
without fragile reflection/string matching. Extract it:

```go
// buildNotifyAdapters returns the ordered detect-install notify adapter table.
// Extracted from connectNotifySetup so tests can assert the registered adapters
// (ids/predicates) without reaching into connect's runtime.
func buildNotifyAdapters() []detectInstallAdapter {
    return []detectInstallAdapter{
        {id: shim.AdapterCodexNotify, label: "Codex", step: "N5",
            fileNote: "~/.codex/config.toml updated",
            targeted: codexTargeted, detect: shim.RemoteHasCodex,
            install: shim.EnsureRemoteCodexNotifyConfig},
        {id: shim.AdapterAntigravityNotify, label: "Antigravity", step: "N5.5",
            fileNote: "cc-clip-notify agy plugin installed",
            targeted: agyTargeted, detect: shim.RemoteHasAgy,
            install: shim.EnsureRemoteAntigravityPlugin},
        {id: shim.AdapterOpencodeNotify, label: "opencode", step: "N5.7",
            fileNote: "cc-clip opencode notify plugin installed",
            targeted: opencodeNotifyTargeted, detect: shim.RemoteHasOpencode,
            install: shim.EnsureRemoteOpencodePlugin},
    }
}
```

`connectNotifySetup` then calls `notifyAdapters := buildNotifyAdapters()`. Task 9 asserts
on `buildNotifyAdapters()` (id present, predicate is `opencodeNotifyTargeted`) — a behavior
test, no reflection. This is a pure extract-function refactor of the Step 6 table; the
Codex/agy rows move verbatim.

`cmd/cc-clip/targets.go`:
```go
// opencodeNotifyTargeted gates ONLY the opencode notify plugin. opencode's
// clipboard already works via the shim (shimTargeted includes Opencode).
func opencodeNotifyTargeted(t DeployTargets) bool { return t.Opencode }
```

`cmd/cc-clip/main.go` — the opencode row goes inside `buildNotifyAdapters()` (see "Test seam" above; the row is shown there). In `connectNotifySetup`, after `adapterOutcomes := runDetectInstallAdapters(...)`, read the opencode outcome and add `notifyOutcome.opencodeAttempted/opencodeInstalled`, the outcome mapping, and:
```go
applyAdapterState(state.Notify, shim.AdapterOpencodeNotify, o.opencodeAttempted, o.opencodeInstalled)
```

---

## TDD task breakdown (ordinal, no time estimates)

- [ ] **Task 1 — Runner dispatch** (`internal/plugin/plugin_test.go`): RED test `Run("opencode-notify", port, stdin, stdout)` reaches `runOpencodeNotify`; unknown-name unchanged. → `go test ./internal/plugin/ -run TestRun`.
- [ ] **Task 2 — Parser** (`internal/plugin/notify_test.go`): table test `parseOpencodeNotifyPayload` — `{"event":{"type":"session.idle"}}` → `{Title:"opencode",Urgency:1,Verified:true,Body:"Session idle…"}`; malformed JSON → error. → `go test ./internal/plugin/ -run TestOpencode`.
- [ ] **Task 3 — Runner fail-soft**: empty/garbage stdin → `runOpencodeNotify` returns nil, no panic, no POST on parse error.
- [ ] **Task 4 — Detector** (`internal/shim/opencode_test.go`): mock executor yes/no/garbage → `RemoteHasOpencode` tri-state; probe string contains `command -v opencode`, not a dir check.
- [ ] **Task 5 — Installer script** (`internal/shim/opencode_test.go`): assert `EnsureRemoteOpencodePlugin` script (a) `mkdir -p ~/.config/opencode/plugins`, (b) mktemp+mv atomic, (c) writes `cc-clip-notify.js`, (d) embeds `cc-clip plugin run opencode-notify`; non-default port → `env CC_CLIP_PORT=<port>` baked in. Assert the embedded JS contains the official `event` hook + `session.idle`.
- [ ] **Task 6 — Strip helper (NOT wired)** (review P1): `StripRemoteOpencodePlugin` issues `rm -f` of the dropped file; no-op when absent. Unit-test the emitted command only. **Do NOT call it from connect/uninstall** — there is no `--opencode` uninstall branch and Step 7 does not add one ("Scope: uninstall"). It is a symmetric helper for the future uninstall-split.
- [ ] **Task 7 — Gating** (extend `notify_targets_test.go`): `--opencode` populates `opencode-notify` deploy-state + leaves claude/codex untouched; `--claude` preserves an existing opencode-notify entry (5.3c). Reuse the `.run`/`mergeNotifyDeployState` patterns from the agy tests.
- [ ] **Task 8 — deploy-state round-trip** (`deploy_test.go`): after `--opencode`, `AdapterInstalled(AdapterOpencodeNotify)` true and survives marshal/unmarshal; `Verified=false` (install ≠ hook-fire proof).
- [ ] **Task 9 — Connect-table wiring test** (review P2): first extract `buildNotifyAdapters()` (pure refactor of the Step 6 local slice — Codex/agy rows move verbatim, `connectNotifySetup` calls `buildNotifyAdapters()`). Then assert `buildNotifyAdapters()` contains the `opencode-notify` row whose `targeted` predicate returns true under `--opencode` and false under `--claude`-only. Behavior test on the seam, no reflection/string-matching.
- [ ] **Task 10 — JS-syntax check (default) + skip-guarded real smoke (manual)** (review P2): two layers, because the original single real-smoke design risked breaking opencode auth (isolated HOME) and incurring real model-call cost.
  - **Layer A — runs by default** (`internal/shim/opencode_test.go`, gated on `bun` or `node` on PATH, else `t.Skip`): write `opencodePluginJS(port)` to a temp file, run `node --check <file>` (or `bun build --no-bundle`) to prove the generated plugin is syntactically valid JS. Cheap, deterministic, no opencode, no model cost. Closes most of the "wrote the file but it's malformed" risk.
  - **Layer B — manual only** (double-gated `CC_CLIP_OPENCODE_SMOKE=1` + `opencode` on PATH): drop the plugin, trigger a real `session.idle`, confirm `cc-clip plugin run opencode-notify` is invoked. **This needs working opencode auth and MAY incur real model-call cost — it is explicitly NOT a CI test;** state that in a comment at the top of the test. opencode auth/data may live outside `XDG_CONFIG_HOME`, so isolating HOME can hide real auth — skip cleanly (not fail) when auth is absent. Layer B is the human-run proof that opencode actually loads + fires.
- [ ] **Task 11 — docs**: README (en/zh/ja) opencode Notifications cell → auto-configured via `--opencode`/`--all`; design doc Step 7 row → done with commit refs.
- [ ] **Task 12 — gates**: `gofmt -l`, `go vet`, `staticcheck`, `go test ./... -race -count=1` (GOCACHE=/private/tmp/cc-clip-go-build), 80%+ coverage on touched packages.

---

## Coordinated-changes checklist (per CLAUDE.md "Changing hook injection")

- `internal/plugin/plugin.go` (const + switch) · `internal/plugin/notify.go` (runner + parser + body)
- `internal/shim/opencode.go` (detect + install + strip) · `internal/shim/opencode_plugin_template.go` (JS template + port)
- `cmd/cc-clip/targets.go` (`opencodeNotifyTargeted`) · `cmd/cc-clip/main.go` (table row + outcome fields + applyAdapterState + uninstall wiring)
- No `daemon/envelope.go` or `daemon/classifier.go` change — opencode-notify produces `KindGenericMessage` exactly like codex/agy.

---

## Non-blocking backlog (do NOT bundle into Step 7 unless asked)

- Permission/error event mappings — once opencode's literal event-bus type strings stabilize (`permission.asked`/`permission.replied`?).
- HTTP-POST fallback from JS (read nonce, POST `127.0.0.1:<port>/notify`) IF a future opencode version drops BunShell `$` stdin support.
- agy: real remote Stop hook-fire smoke + `agy plugin uninstall cc-clip-notify` uninstall path.
- Project-wide hardening: absolute `$HOME/.local/bin/cc-clip` in claude/codex/agy/opencode hook commands (verify exec model first via hook-fire smokes).
- Design doc cleanup: scattered `--antigravity`/`antigravity-notify` at lines ~39/64/94/130/236/279; README.ja.md:166 Chinese semicolon `；`.
- Step 8: npm/npx bootstrap distribution.
