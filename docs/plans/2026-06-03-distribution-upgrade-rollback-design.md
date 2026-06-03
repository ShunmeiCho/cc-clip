# Distribution, Upgrade & Rollback — Process & Design

> **Status: DESIGN / ANALYSIS (for review, rev 2 after review).** No implementation here. Grounded in a file:line audit of the current machinery.
> **Branch:** `docs/dist-upgrade-rollback-design` (off `main` @ `3c96a18`).
> **Why now:** v0.9.0 did a large deployment refactor (target split, settings-first hooks, per-adapter deploy-state, `--codex` breaking change). **v0.9.0 has not been publicly tagged yet** — golden window to add the migration/rollback affordances below *before* any user deploys state that lacks them.

---

## Current state (what already EXISTS — do NOT reinvent)

- **Upgrade:** `docs/upgrading.md` (version discovery, per-machine matrix, 3 install paths, daemon/port checks, rollback section). `cc-clip update [--to vX.Y.Z]` (`update.go`) with checksum-verify → `.bak` → swap → `/health` verify → **auto-rollback on failure** (`update.go:157-332`). Idempotent settings-first hook migration keyed off the permanent `CC_CLIP_MANAGED=1` prefix (`settings.go:13-30,225-227`). Legacy `~/.local/bin/claude` wrapper auto-removal restoring `.cc-clip-real` (`ssh.go:693-705`). deploy-state legacy→per-adapter migration on read (`deploy.go:121-141`). Hash-based re-deploy (`deploy.go:217-237`) + actual-remote-file re-verify (`main.go:872-914`). `--auto-recover` for the v0.7.0 wrapper hazard (`main.go:725-791`).
- **Rollback:** `cc-clip update --to vX.Y.Z` pins any tag with full local rollback. GitHub Releases immutable per-tag (`cc-clip_{version}_{os}_{arch}.tar.gz` + `checksums.txt`, `.goreleaser.yaml:21-37`). Hosts registry persists `LastDeployedVersion` (`hosts/registry.go:45`). "Never uninstall an existing shim" (`main.go:894-898`).
- **Distribution:** `scripts/install.sh` (os/arch detect, checksum verify `:50-75`, Gatekeeper re-sign). Asset-naming contract triplicated across goreleaser/install.sh/update.go, guarded by `make release-preflight` + `release.yml:25-44`.
- **Process:** tag → goreleaser (`release.yml`), PR CI (build/vet/test-race/staticcheck/govulncheck, `ci.yml`), cross-arch connect delivery (`main.go:1696-1817`).

## Gaps (file:line)

| Concern | Gap |
|---|---|
| Upgrade migration | deploy-state has **no schema-version field** (`deploy.go:73`); migration is structural inference. `deploy.json` overwritten with **no backup** (`deploy.go:180`). |
| Rollback | `install.sh` **cannot pin a version** (always `/latest`, `:77`). **No remote-side binary/state backup** — `~/.local/bin/cc-clip` overwritten in place (`main.go:886`). **A pre-v0.9.0 binary cannot be made to fail-closed** on a newer state (it predates any guard). |
| Supply chain | cross-arch connect's `downloadReleaseBinary` does `curl + tar` with **NO checksum verification** (`main.go:1798-1804`) — weaker than install.sh/update. Checksums are same-origin (transport integrity, not provenance). |
| npm/npx | **Zero scaffolding**. install.sh has **no Windows** + no pinning. |
| Process | release gate `release.yml` runs only `make test` + contract grep (`:23`); **weaker than PR CI** (`ci.yml:25`). No migration/downgrade fixture as a release gate. |

---

## Concern 1 — Upgrade / migration (old → v0.9.0+)

**Mostly works:** `cc-clip connect <host> --force` from a new local binary converges an old remote (hash re-upload, idempotent hook merge, wrapper auto-removal, deploy-state migration). True hazard: v0.7.0 NonRecoverable wrapper state (handled by `--auto-recover`).

**P0 — TWO parts (the guard alone is insufficient):**
1. Add `DeployState.SchemaVersion int` (`deploy.go`). A v0.9.0+ binary writes/reads it and can refuse to clobber a *newer* state (forward downgrade protection — only for binaries that ship WITH the guard).
2. **Explicit cross-v0.9 downgrade runbook + cleanup** in `docs/upgrading.md`. `SchemaVersion` CANNOT make an already-released v0.8.x binary "understand and refuse" v0.9.0 state — that old binary has no guard and will whole-file-overwrite `deploy.json`, dropping `Adapters` / `agy-notify` / `opencode-notify`. So the runbook must tell cross-v0.9 downgraders to expect/clean per-adapter state, not rely on a fail-closed.

## Concern 2 — Rollback (buggy new version → known-good)

**Backbone exists:** `update --to vOLD` (local auto-rollback) + immutable tags.

**P0 additions:**
- `install.sh` version pinning — honor `CC_CLIP_VERSION=vX.Y.Z` (and/or `--version`) to fetch a specific tag, not just `/latest`. Mirrors `update --to`; first-class curl-install rollback channel. MUST keep the asset-naming contract.
- Fix `docs/upgrading.md` rollback section: (a) the stale `deploy-state.json` → `deploy.json` (corrected on PR #88; ensure it lands), and (b) **stop framing "downgrade then `connect --force`" as universally lossless** — same-generation rollback is the normal path, but cross-v0.9 rollback needs the cleanup from Concern 1.

**P1 additions:**
- Remote `deploy.json` one-deep backup (`deploy.json.bak`) on `WriteRemoteState`.
- (Optional) remote binary sidecar (`cc-clip.bak`) mirroring the local `.bak`. Evaluate need vs. complexity; likely defer.

**Rollback RUNBOOK (document):**
1. Local: `cc-clip update --to vGOOD` (auto-verifies + rolls back) — or `CC_CLIP_VERSION=vGOOD` install.sh once P0 lands.
2. Per host: `cc-clip connect <host> --force` (re-uploads good binary; owner-prefix union cleanly replaces the bad managed command). `cc-clip hosts list` (`LastDeployedVersion`) drives fan-out.
3. **Cross-v0.9 caveat:** an older binary does NOT understand `--opencode`/`--agy`/per-adapter state; clean/redeploy those before relying on it.
4. Verify: `cc-clip doctor <host>` / `/health`.

## Concern 3 — npm / npx distribution (REVISED main approach)

**Main approach = entry package + per-platform binary packages (NOT bootstrap-download).** Follow the esbuild/@swc/turbo pattern:
- An entry package (`cc-clip`) with `optionalDependencies` on per-platform packages (`@cc-clip/darwin-arm64`, `@cc-clip/linux-amd64`, `@cc-clip/win32-x64`, …), each declaring `os`/`cpu` so npm installs ONLY the matching platform binary. The entry `bin` resolves to the installed platform package's binary. npm pinning is intrinsic (`npm i -g cc-clip@0.9.0` / `npx cc-clip@0.9.0` → the matching version).
- **GitHub-Release bootstrap-download is a FALLBACK/rescue path only** (e.g. unsupported platform or a `postinstall` that fetches+verifies the asset). Not the primary mechanism.
- `npx` = one-shot / version-pinned rescue (it is `npm exec`; remote packages carry prompt/cache semantics). `npm i -g` = long-term install.
- **dist-tags:** stable → `latest`; prereleases → `next`/`beta` (never publish a prerelease as `latest`). **Bad version → `npm deprecate`, NOT unpublish** (unpublish breaks reproducibility/lockfiles).
- Map npm semver ↔ GitHub tag; the per-platform packages carry the same binary goreleaser produces.

**Leave-room measures (before publishing):**
- Asset-naming string stays the ONE contract; when npm lands, add its references to `make release-preflight` + `release.yml` grep so goreleaser ↔ install.sh ↔ update.go ↔ npm stay in lockstep.
- Make Windows assets first-class in goreleaser naming (install.sh omits Windows; npm's main value is cross-platform incl. Windows).
- Keep binary `--version`/checksum stable.

## Concern 4 — Supply-chain integrity (P1)

- **Fix the cross-arch gap:** `downloadReleaseBinary` (`main.go:1798-1804`) must verify `checksums.txt` like install.sh/update do — currently it does not, so the cross-arch connect path trusts an unverified tarball.
- **Beyond checksum (same-origin):** plan provenance, not just integrity — npm **trusted publishing / provenance** when npm lands; GitHub Release **signing / build attestation** (e.g. `gh attestation` / cosign) so the binary's origin is verifiable, not just its transport.

## Concern 5 — Process / 流程对策

- **Runbooks** in `docs/upgrading.md`: upgrade + rollback (per Concerns 1–2), per-host fan-out via hosts registry, the v0.7.0 hazard, and the cross-v0.9 caveat.
- **Sync the release gate to PR-CI level:** `release.yml` currently runs only `make test` + contract grep; before v0.9.0 it should also run build/vet/test-race/staticcheck/govulncheck (match `ci.yml`), **plus** a state-migration + downgrade-protection fixture (legacy `deploy.json` → new binary round-trip; newer-state-vs-older-binary guard).
- **Release sequencing:** land P0 (`SchemaVersion`, install.sh pinning, upgrading.md runbook fixes) **before** the first public v0.9.0 tag.
- **release-notes requirement (if promising rollback):** v0.9.0 notes MUST state — same-generation rollback is a normal path; **cross-v0.9 downgrade requires cleanup/redeploy** because the older binary does not understand `--opencode`/`--agy`/per-adapter state.
- `make release-preflight` stays the single contract guard; extend as npm lands.

---

## Prioritized recommendations

- **P0 (before public v0.9.0 tag):** (1) `DeployState.SchemaVersion` + forward downgrade guard; (2) `install.sh` `CC_CLIP_VERSION` pinning; (3) `docs/upgrading.md` — fix `deploy.json` name + cross-v0.9 rollback runbook (no false "lossless `--force`" promise).
- **P1:** cross-arch `downloadReleaseBinary` checksum verify; sync `release.yml` to PR-CI gate + add migration/downgrade fixture; remote `deploy.json.bak`; document v0.7.0 hazard prominently.
- **P2:** npm/npx — entry+platform-packages design (optionalDependencies, os/cpu), bootstrap as fallback, dist-tags + deprecate policy, provenance; publish when ready; wire naming into release-preflight. Optional remote binary sidecar (evaluate first).

## Out of scope / non-goals

- A bespoke updater protocol — `update --to` + immutable tags cover versioned forward/back.
- Auto-downgrade on bug detection — rollback stays an explicit operator action.
- Changing the asset-naming contract — load-bearing; only ADD channels that match it.
- Migrating away from the SSH/RemoteForward model.

## Recommended execution order

1. **P0 (immediate, pre-tag):** `SchemaVersion`, install.sh pinning, upgrading.md runbook + `deploy.json` fix.
2. **P1:** cross-arch checksum verify, release-gate sync + migration/downgrade fixture, `deploy.json.bak`.
3. **npm/npx:** revise to entry+platform-packages design first; implement later.
4. Do NOT advance multi-target or npm code implementation ahead of P0.
