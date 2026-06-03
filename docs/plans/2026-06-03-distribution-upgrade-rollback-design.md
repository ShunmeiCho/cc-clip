# Distribution, Upgrade & Rollback — Process & Design

> **Status: DESIGN / ANALYSIS (for review).** No implementation here. Grounded in a file:line audit of the current machinery (see "Current state").
> **Branch:** `docs/dist-upgrade-rollback-design` (off `main` @ `3c96a18`).
> **Why now:** v0.9.0 did a large deployment refactor (target split, settings-first hooks, per-adapter deploy-state, `--codex` breaking change). **v0.9.0 has not been publicly tagged yet** — this is the golden window to add the migration/rollback affordances below *before* any user deploys a state file that lacks them.

---

## Current state (what already EXISTS — do NOT reinvent)

The machinery is more complete than a fresh design would assume:

- **Upgrade:** `docs/upgrading.md` (228 lines: version discovery, per-machine matrix, 3 install paths, daemon-restart/port checks, rollback section). `cc-clip update [--to vX.Y.Z]` self-update (`cmd/cc-clip/update.go`) with checksum-verify → `.bak` backup → swap → `/health` verify → **auto-rollback on failure** (`update.go:157-332`). Idempotent settings-first hook migration keyed off the permanent `CC_CLIP_MANAGED=1` prefix (`internal/shim/settings.go:13-30,225-227`). Legacy `~/.local/bin/claude` wrapper auto-removal restoring the `.cc-clip-real` sidecar (`ssh.go:693-705`). deploy-state legacy→per-adapter migration on read (`deploy.go:121-141`). Hash-based re-deploy (`deploy.go:217-237`) + actual-remote-file re-verify (`main.go:872-914`). `--auto-recover` for the v0.7.0 wrapper-corruption hazard (tri-state, fail-closed, TOCTOU-guarded; `main.go:725-791`, `ssh.go:625-639`).
- **Rollback:** `cc-clip update --to vX.Y.Z` pins any tag with full local rollback. GitHub Releases are immutable per-tag (`cc-clip_{version}_{os}_{arch}.tar.gz` + `checksums.txt`, `.goreleaser.yaml:21-37`, `draft:false`). Hosts registry persists `LastDeployedVersion` per host (`internal/hosts/registry.go:45`) for redeploy fan-out. "Never uninstall an existing shim" (`main.go:894-898`).
- **Distribution:** `scripts/install.sh` (os/arch detect `:10-26`, checksum verify `:50-75`, install dir `${CC_CLIP_INSTALL_DIR:-$HOME/.local/bin}`, macOS Gatekeeper re-sign `:115-118`). Asset-naming contract is the load-bearing constant, triplicated across goreleaser / install.sh / update.go and guarded by `make release-preflight` + `.github/workflows/release.yml:25-44`.
- **Process:** tag → goreleaser (`release.yml`), PR CI (build/vet/test-race/staticcheck/govulncheck), cross-arch connect delivery (`main.go:1696-1817`).

## Gaps the four concerns map to

| Concern | Gap (file:line) |
|---|---|
| Upgrade migration | deploy-state has **no schema-version field** (only `BinaryVersion`); migration is structural inference (`Adapters==nil`⇒legacy, `deploy.go:121-141`). NEW state read by OLD binary is **lossy-silent** (drops unknown fields); **no guard** stops an older local binary clobbering a newer remote state. `deploy.json` overwritten with **no backup** (`deploy.go:181-196`). |
| Rollback | `install.sh` **cannot pin a version** (always `/latest`, `:78`) — install.sh rollback is manual Option-C curl. **No remote-side binary backup** — `~/.local/bin/cc-clip` overwritten in place (`main.go:886`); remote rollback requires a local downgrade first (manual, doc-driven). |
| npm/npx | **Zero scaffolding** (no `package.json`/`bin/`). install.sh has **no Windows** support and no version pinning — both required for `npx cc-clip@x.y.z`. |
| Process | **No migration/convergence gate in CI** — only asset-naming drift is checked; `migrateNotifyState` round-trip + downgrade-protection are unit-tested only, not release-gated. |

---

## Concern 1 — Upgrade / migration (old deployment → v0.9.0+)

**Mostly already works:** re-running `cc-clip connect <host> --force` from a new local binary converges an old remote — hash-mismatch re-upload, idempotent hook merge, wrapper auto-removal, deploy-state migration. The one true hazard is v0.7.0 NonRecoverable wrapper state (handled by `--auto-recover` + fail-closed manual reinstall).

**Recommended addition (P0 — golden window):**
- Add `SchemaVersion int` to `DeployState` (`deploy.go`). NEW binary writes the current schema number. This makes future migrations explicit instead of structural-inference-only, and enables the downgrade guard below.

**Process (document in upgrading.md):**
- Old → v0.9.0: `cc-clip update` (or install.sh) to get the new local binary → `cc-clip connect <host> --force` per host (the `--force` is the documented belt-and-suspenders against hash-skip). The hook/wrapper/state migration is automatic and idempotent.
- Surface the v0.7.0 hazard + `--auto-recover` prominently in the upgrade doc.

## Concern 2 — Rollback (buggy new version → known-good)

**Backbone already exists:** `cc-clip update --to vOLD` (local, full auto-rollback) + immutable GitHub tags. Gaps are install.sh pinning and remote independence.

**Recommended additions:**
- **P0:** `install.sh` version pinning — honor `CC_CLIP_VERSION=vX.Y.Z` (and/or `--version`) to fetch a specific tag, not just `/latest`. Mirrors `update --to`; makes the curl-install path a first-class rollback channel. MUST keep the asset-naming contract.
- **P1:** remote `deploy.json` one-deep backup (`deploy.json.bak`) on `WriteRemoteState` — lets a botched connect's prior state be inspected/restored without a full re-derive.
- **P1 (optional):** evaluate a remote binary sidecar (`cc-clip.bak`) mirroring the local `.bak` pattern, so a bad remote binary can be reverted in place. Trade-off: disk + complexity vs. the current "re-run connect from older local binary" path. Likely defer unless remote-only rollback is a real need.

**Rollback RUNBOOK (document in upgrading.md):**
1. Local: `cc-clip update --to vGOOD` (auto-verifies + rolls back on failure) — or `CC_CLIP_VERSION=vGOOD` install.sh once P0 lands.
2. Per host: `cc-clip connect <host> --force` (re-uploads the good binary; idempotent hook merge + owner-prefix union cleanly replace the bad managed command). Use `cc-clip hosts list` (`LastDeployedVersion`) to drive the fan-out.
3. Verify: `cc-clip doctor <host>` / `/health`.

## Concern 3 — npm / npx distribution prep ("leave room")

Goal: future `npm i -g cc-clip` / `npx cc-clip@x.y.z` without a second source of truth.

**Design (scaffold now, publish later):**
- `package.json` with a `bin` entry → a Node bootstrap (`postinstall` or lazy on first `bin` run) that **reuses the existing contract**: GitHub Releases URL + `cc-clip_{version}_{os}_{arch}.tar.gz` + `checksums.txt` verify. The npm semver maps to the GitHub tag (`npx cc-clip@0.9.0` → tag `v0.9.0`); pinning is intrinsic to npm (unlike install.sh's `/latest`).
- **Reuse:** os/arch normalization, checksum verify, macOS Gatekeeper re-sign (port install.sh's logic to Node). **Add:** Windows os/arch (install.sh omits it; npm's main value is cross-platform install — `.goreleaser.yaml` already builds a zip for windows per the release-contract grep).
- **Leave-room measures (do these even before publishing):**
  - Treat the asset-naming string as the ONE contract; when the npm bootstrap lands, **add its asset-name reference to the `make release-preflight` + `release.yml` contract grep** so goreleaser ↔ install.sh ↔ update.go ↔ npm stay in lockstep.
  - Ensure Windows assets are first-class in goreleaser naming so the npm wrapper can target them.
  - Keep the binary `--version` / checksum behavior stable (the npm bootstrap depends on both).

## Concern 4 — Process / 流程对策 (emphasis)

- **Upgrade runbook + Rollback runbook** in `docs/upgrading.md` (per Concerns 1–2 above), including the per-host fan-out via the hosts registry and the v0.7.0 hazard.
- **CI release gate — migration convergence:** add a release-gated test (beyond the existing unit tests) that (a) a legacy boolean-only `deploy.json` → NEW binary read → `migrateNotifyState` round-trips, and (b) **downgrade protection**: a binary with `SchemaVersion < remote` refuses to clobber (warn/abort or require `--force-downgrade`). This is the safety net for "new version is buggy."
- **Release sequencing:** land P0 (deploy-state `SchemaVersion` + install.sh pinning) **before** the first public v0.9.0 tag — once users deploy a state file without the version field, downgrade protection can only be best-effort.
- **release-preflight** stays the single contract guard; extend it as npm lands.

---

## Prioritized recommendations

- **P0 (before public v0.9.0 tag):** (1) `DeployState.SchemaVersion` + downgrade-protection guard; (2) `install.sh` version pinning via `CC_CLIP_VERSION`; (3) upgrade + rollback runbooks in `upgrading.md`.
- **P1:** remote `deploy.json.bak`; CI migration-convergence + downgrade-protection gate; document the v0.7.0 hazard prominently.
- **P2:** npm/npx package scaffolding (bin bootstrap reusing the asset contract + Windows + checksum/Gatekeeper) — publish when ready; wire its naming into release-preflight. Optional remote binary sidecar (evaluate need first).

## Out of scope / non-goals

- A bespoke updater protocol — `update --to` + immutable GitHub tags already cover versioned forward/back.
- Auto-downgrade on bug detection — rollback stays an explicit operator action.
- Changing the asset-naming contract — it is load-bearing; only ADD channels that match it.
- Migrating away from the SSH/RemoteForward model.
