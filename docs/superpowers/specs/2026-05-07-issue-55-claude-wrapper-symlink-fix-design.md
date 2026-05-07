# Issue #55 — Claude Wrapper Symlink-Safe Install Fix

**Date:** 2026-05-07
**Issue:** [#55](https://github.com/ShunmeiCho/cc-clip/issues/55) — Setup overwrites claude binary at symlink target instead of replacing the symlink
**Affected version:** v0.7.0
**Target release:** v0.7.1

---

## 1. Problem

### 1.1 Reported symptom

On remotes where Claude Code is installed via Anthropic's official Native Installer (`curl https://claude.ai/install.sh`), the layout is:

```
~/.local/bin/claude                       -> ~/.local/share/claude/versions/X.Y.Z   (symlink)
~/.local/share/claude/versions/X.Y.Z      (250MB ELF binary, the real claude)
```

After `cc-clip setup myhost` (or `cc-clip connect myhost`) completes:

```
~/.local/bin/claude                       -> ~/.local/share/claude/versions/X.Y.Z   (symlink unchanged)
~/.local/share/claude/versions/X.Y.Z      (1017-byte cc-clip wrapper bash script)   ← CORRUPTED
~/.local/bin/claude.cc-clip-bak           (the original 250MB binary)
```

Subsequent `claude` invocation:

```
$ claude --resume
cc-clip: real claude binary not found in PATH
```

### 1.2 Root cause — two symlink-follow defects in `InstallRemoteClaudeWrapper`

Code reference: `internal/shim/ssh.go:263-278`.

| Step | Command on remote | Behavior on a symlink |
|---|---|---|
| 1. Detect existing wrapper | `head -5 ~/.local/bin/claude` | Follows symlink → reads first 5 lines of 250MB ELF (binary garbage). String `cc-clip claude wrapper` is absent → "needs backup" branch is taken. |
| 2. Backup | `cp ~/.local/bin/claude ~/.local/bin/claude.cc-clip-bak` | `cp` without `-d`/`-P` follows the symlink at the source, so the 250MB binary content is copied into `claude.cc-clip-bak`. |
| 3. **Write wrapper (CRITICAL)** | `cat > ~/.local/bin/claude` | Shell `>` is `open(O_WRONLY\|O_CREAT\|O_TRUNC)` — it **follows the symlink** and truncates/writes to `~/.local/share/claude/versions/X.Y.Z`. Wrapper script (1017 bytes) lands inside the versions store, overwriting the real binary. |
| 4. Permissions | `chmod +x ~/.local/bin/claude` | Follows symlink (no-op; the file was already +x). |

### 1.3 Why the wrapper's PATH-discovery fallback fails

`internal/shim/claude_wrapper.go:14-17` iterates `$PATH` looking for `claude` and skips its own directory. Under Native Installer layout there is **no second `claude`** elsewhere on PATH — `~/.local/bin` is the only entry that holds it. The wrapper exits 1 with "real claude binary not found in PATH".

### 1.4 Blast radius

- **Severity:** HIGH. Silently corrupts a 250MB user-owned binary.
- **Affected users:** Every cc-clip user whose remote uses Anthropic's recommended Native Installer (the most common path).
- **Detectability:** None at install time. Only surfaces when the user runs `claude` after `cc-clip setup`.
- **Recoverability:** Manual `mv` of the backup; documented as the issue's workaround.
- **Compounding risk:** Anthropic's installer self-update may `ln -sf ~/.local/bin/claude → versions/X.Y.Z+1`, replacing whatever lives at `~/.local/bin/claude`. **Any** wrapper-at-symlink-path strategy must accept "user re-runs `cc-clip setup` after Claude Code upgrade" as the recovery contract; this is unavoidable and orthogonal to the bug being fixed here.

---

## 2. Solution overview — Approach B (symlink-safe with explicit sidecar + opt-in v0.7.0 recovery)

The fix has six layers, applied together: install topology, wrapper exec topology, corruption detection + recovery, symmetric uninstall, DeployState integration, and a test seam for unit-testability.

### Layer 1 — Symlink-safe install topology

Replace the install sequence in `InstallRemoteClaudeWrapper` with a state-aware flow that uses **`mv` only** (never `cp`/`>` against a path that may be a symlink):

```text
Install algorithm (prepare-then-commit, with rollback)
──────────────────────────────────────────────────────
1.  Classify ~/.local/bin/claude (read-only inspection, no writes):
      - none        : path does not exist
      - cc_wrapper  : test -f && ! test -L, AND head -c 256 contains
                      "# cc-clip claude wrapper" (the marker)
      - symlink     : test -L returns 0
      - regular     : test -f returns 0, ! test -L, and not cc_wrapper
      - other       : refuse to install (e.g. directory, device, socket)

2.  Prepare wrapper tmp file FIRST (before touching the origin):
      a. mkdir -p ~/.local/bin
      b. tmp=$(mktemp "$HOME/.local/bin/.claude.cc-clip-tmp.XXXXXX")
         WHY mktemp: it uses O_CREAT|O_EXCL with a random suffix, so it
         CANNOT follow a pre-existing symlink — even if an attacker or a
         botched prior install left a symlink at any predictable tmp path,
         mktemp would fail rather than write through. Predictable names
         like ".cc-clip-tmp.$$" do NOT have this guarantee.
         WHY leading dot: hides the tmp from `which claude` PATH-discovery
         during the brief window it lives next to the real claude.
      c. cat > "$tmp"                  (safe: $tmp is a fresh regular file
                                        with no symlink semantics possible)
      d. chmod +x "$tmp"

      On any failure in steps 2a-d:
          rm -f "$tmp"; return error.
          The origin is still in place; no user-visible change.

3.  Stage origin and commit wrapper, branch by classification:

      Case "none":
        mv "$tmp" ~/.local/bin/claude
        # No sidecar to create.

      Case "cc_wrapper" (re-install over our own previous wrapper):
        mv "$tmp" ~/.local/bin/claude
        # Existing sidecar (if any) is left untouched.

      Case "symlink" or "regular":
        # Pre-flight: sidecar collision guard.
        # If ~/.local/bin/claude.cc-clip-real already exists, we don't know whether
        # it is stale state from a prior cc-clip install (e.g. user manually
        # removed the wrapper but left the sidecar) or genuinely-needed state.
        # Refuse rather than overwrite — user can `rm` the sidecar and re-run.
        # Critically: if it's a directory, `mv claude .cc-clip-real` would move
        # claude INTO the directory rather than rename to it, silently breaking
        # wrapper exec. Refusing here avoids that footgun.
        if test -e ~/.local/bin/claude.cc-clip-real || test -L ~/.local/bin/claude.cc-clip-real; then
            rm -f "$tmp"
            return error with diagnostic explaining the conflict and suggesting
            `rm ~/.local/bin/claude.cc-clip-real` to proceed.
        fi

        # Stage origin to sidecar:
        if ! mv ~/.local/bin/claude ~/.local/bin/claude.cc-clip-real; then
            # Origin was never moved. Just clean up tmp and exit.
            rm -f "$tmp"
            return error (origin unchanged; user's claude still works).
        fi

        # Commit wrapper:
        if ! mv "$tmp" ~/.local/bin/claude; then
            # Best-effort rollback to leave user with working claude:
            mv ~/.local/bin/claude.cc-clip-real ~/.local/bin/claude
            rm -f "$tmp"
            return error
        fi
        # On success: origin lives at sidecar, wrapper lives at claude.

      Case "other":
        rm -f "$tmp"
        return error with diagnostic.

Hard invariants enforced by this ordering:
  - `cat >` is only ever applied to a freshly-created mktemp regular file.
    It is NEVER applied to a path that may be a user-owned symlink.
  - The window during which `~/.local/bin/claude` is missing is bounded to
    one mv between staging the origin and committing the wrapper.
  - On failure inside the symlink/regular branch AFTER staging, rollback
    restores the origin so the user's `claude` keeps working — even if
    cc-clip wrapper installation did not succeed.
  - On failure BEFORE staging (steps 2a-d), the origin was never touched.
```

Key invariant: **`>` redirection is only ever applied to a freshly-created mktemp regular file or to a path we have just verified is a cc-clip-owned regular file (cc_wrapper re-install). It is NEVER applied to a path that might be a user symlink, and the tmp path is never predictable.**

### Layer 2 — Wrapper exec topology (sidecar-first)

Update `claudeWrapperTemplate` so the bash wrapper resolves the real claude binary in this priority:

```text
1. If `~/.local/bin/claude.cc-clip-real` is executable (regular file or working symlink):
       _REAL_CLAUDE=~/.local/bin/claude.cc-clip-real
2. Else fall back to current PATH-discovery (skip self_dir, walk $PATH):
       _REAL_CLAUDE=$(first claude on PATH outside self_dir)
3. Else exit 1 with diagnostic.
```

The fallback preserves backward compatibility for users who installed under earlier cc-clip versions where there is no sidecar. Wrapper still does **not** parse JSON or read deploy state — it only checks file existence/executability.

### Layer 3 — v0.7.0 corruption: detect, default fail-closed, opt-in recovery

`setup` and `connect` invoke a new `DetectV070Corruption(session)` step **immediately after SSH session establishment, before any other remote write** (we call this step **N0**). Concretely: it runs after `EnsureSession`/SSH master setup but before binary upload (`cmd/cc-clip/main.go` ~line 570), shim install (~line 660), token sync, hook script install, and wrapper install (line 811-824).

The reason for placing detection at N0: the corruption recovery default is **fail closed**. Running detection at N4 (the wrapper-install step) would leave a half-deployed remote on abort: binary uploaded, xclip shim installed, hook script installed, but wrapper aborted. Placing it at N0 guarantees that **fail-closed leaves zero new state on the remote** — the existing v0.7.0 corruption is also untouched. On `--auto-recover`, recovery runs at N0, then the normal deploy flow proceeds end-to-end without any awkward mid-flow branch.

The detection requires **all five** conditions to be true to flag a remote as "v0.7.0 corrupted":

```text
C1. test -L ~/.local/bin/claude                     (claude is a symlink)
C2. resolved = $(readlink -f ~/.local/bin/claude)
    head -c 256 "$resolved" | grep -q "# cc-clip claude wrapper"
                                                    (symlink target IS a cc-clip wrapper)
C3. test -f ~/.local/bin/claude.cc-clip-bak         (backup exists, regular file)
C4. size of claude.cc-clip-bak > 1048576 (1 MiB)    (backup is plausibly a real binary)
C5. ! head -c 256 ~/.local/bin/claude.cc-clip-bak | grep -qF "# cc-clip claude wrapper"
                                                    (backup is NOT itself a wrapper —
                                                     guards against double-install corruption.

                                                     IMPORTANT: use `! ... | grep -qF MARKER`
                                                     to negate the entire search. `grep -qv`
                                                     is WRONG here: even a real wrapper has
                                                     lines (e.g. the shebang) that do not
                                                     contain the marker, so `grep -qv MARKER`
                                                     would exit 0 and falsely pass C5,
                                                     allowing --auto-recover to mv a wrapper
                                                     back into the versions store.)
```

#### Default behavior (no `--auto-recover` flag)

- Print a diagnostic block listing C1..C5 with check/cross marks per condition.
- Print a copy-paste recovery command and exit non-zero **before** writing any new wrapper or sidecar:

  ```bash
  mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"
  cc-clip setup <host>      # then re-run setup
  ```

- Wrapper install does NOT proceed. The user is explicitly choosing what to do with their Claude binary.

#### `--auto-recover` flag behavior

When the user passes `--auto-recover` to `setup` or `connect`:

1. Re-verify all five conditions (recheck inside the same SSH session — guard against TOCTOU).
2. Execute on remote, in order:
   ```bash
   target=$(readlink -f ~/.local/bin/claude) && \
   mv ~/.local/bin/claude.cc-clip-bak "$target"
   ```
3. Proceed into Layer-1 symlink-safe install (which will then `mv` the symlink itself to `.cc-clip-real` and write a fresh wrapper).

If any of C1..C5 fails, `--auto-recover` does **not** silently install a new wrapper. It aborts with a diagnostic explaining which condition failed and recommends manual recovery (or, for irrecoverable cases like missing backup, recommends reinstalling Claude Code).

#### Flag combination: `--auto-recover` × `--token-only` is mutually exclusive

`--auto-recover` is contractually "recovery + reinstall in one step" (per §6 release notes and §7 decisions log). `--token-only` is contractually "skip binary/shim/wrapper install, just sync the token". These two modes **conflict**: passing both leaves an undefined third mode where backup is migrated but no wrapper is reinstalled — the remote ends up with a working `claude` binary but no notification hooks, with no clear reason for the user to know they're in this state.

Behavior: when both `--auto-recover` and `--token-only` are passed, `setup`/`connect` fail fast at flag-parse time (before SSH session setup, before N0 detection) with:

```
error: --auto-recover cannot be combined with --token-only
       --auto-recover performs recovery and full reinstall.
       Re-run without --token-only:
           cc-clip setup <host> --auto-recover
       Or, if you only want to recover the binary without reinstalling the
       wrapper, run the manual recovery and then `cc-clip setup --token-only`:
           ssh <host> 'mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"'
           cc-clip setup <host> --token-only
```

Exit code: non-zero (e.g. exit 2 for usage errors, matching Go convention).

### Layer 4 — Symmetric uninstall

New function `UninstallRemoteClaudeWrapper(session)`:

```text
1. If ~/.local/bin/claude does not exist: nothing to do.
2. If it is NOT a cc-clip wrapper (head -c 256 lacks the marker): refuse.
   Print a warning. The user has a foreign file there; we won't touch it.
3. If it IS a cc-clip wrapper:
   a. rm ~/.local/bin/claude
   b. If ~/.local/bin/claude.cc-clip-real exists:
        mv ~/.local/bin/claude.cc-clip-real ~/.local/bin/claude
      (This restores the original symlink or regular file as it was before install.)
   c. If ~/.local/bin/claude.cc-clip-bak exists from a v0.7.0 install:
        Print informational note about the legacy backup; do NOT auto-move it.
        (The user's real binary is at the symlink target now if recovery happened;
         if it didn't, the user has a manual decision to make.)
```

Wired into the existing `cmdUninstall --host <host>` branch in `cmd/cc-clip/main.go:371-413`.

CLI surface decision: we extend the existing `cc-clip uninstall --host <host>` flag to also remove the wrapper and restore origin. We do NOT introduce a new positional `cc-clip uninstall <host>` form in v0.7.1 — that would be a CLI-surface expansion beyond the bug fix. Existing semantics of `cc-clip uninstall` (local-only) and `cc-clip uninstall --codex` are unchanged. Only the `--host <host>` branch gains the wrapper-restore step.

#### Required fix to existing cmdUninstall control flow

The current implementation at `cmd/cc-clip/main.go:399` calls `shim.Uninstall(...)` and `log.Fatalf` on error **before** entering the `--host` branch at line 405. This means `cc-clip uninstall --host <host>` on a machine where the local shim is absent (e.g. uninstalled previously, or never installed because the user only used cc-clip from another box) will fatal **before** any remote cleanup runs. Remote wrapper restore would never trigger.

Fix: when `--host != ""`, downgrade the local uninstall from fatal to best-effort. Concretely, replace lines 399-403:

```go
// Before (current):
if err := shim.Uninstall(target, installPath); err != nil {
    log.Fatalf("uninstall failed: %v", err)
}
fmt.Println("Shim removed successfully.")

// After:
if err := shim.Uninstall(target, installPath); err != nil {
    if host == "" {
        log.Fatalf("uninstall failed: %v", err)
    }
    fmt.Fprintf(os.Stderr, "warning: local shim uninstall failed (continuing because --host was set): %v\n", err)
} else {
    fmt.Println("Shim removed successfully.")
}
```

Then in the `--host` branch (line 405-412), add the wrapper restore step before the existing PATH marker cleanup:

```go
if host != "" {
    fmt.Printf("Restoring claude wrapper on remote %s...\n", host)
    session, err := shim.NewSSHSession(host)
    if err != nil {
        fmt.Fprintf(os.Stderr, "warning: failed to open SSH session for wrapper restore: %v\n", err)
    } else {
        defer session.Close()
        if err := shim.UninstallRemoteClaudeWrapper(session); err != nil {
            fmt.Fprintf(os.Stderr, "warning: failed to restore claude wrapper: %v\n", err)
        }
    }
    // Existing PATH marker cleanup follows.
    fmt.Printf("Removing PATH marker from remote %s...\n", host)
    // ... (unchanged)
}
```

Net behavioral change:
- `cc-clip uninstall` (no `--host`): unchanged — still fatal on local error.
- `cc-clip uninstall --host <host>`: local error becomes a warning; remote wrapper restore + PATH marker cleanup always run.

### Layer 5 — DeployState integration

Extend `internal/shim/deploy.go:DeployState` with a new sub-struct:

```go
type ClaudeWrapperState struct {
    Installed    bool   `json:"installed"`
    OriginKind   string `json:"origin_kind"`   // "none" | "regular" | "symlink"
    OriginTarget string `json:"origin_target,omitempty"` // resolved path when OriginKind=="symlink"
}

type DeployState struct {
    // ... existing fields ...
    ClaudeWrapper *ClaudeWrapperState `json:"claude_wrapper,omitempty"`
}
```

Synthetic example of the new field on disk:

```json
{
  "claude_wrapper": {
    "installed": true,
    "origin_kind": "symlink",
    "origin_target": "~/.local/share/claude/versions/X.Y.Z"
  }
}
```

Used by:
- `setup`/`connect` to record what the wrapper replaced (for diagnostics + future doctor command).
- `uninstall` informational output (e.g. "this wrapper originally replaced a symlink → versions/X.Y.Z").
- **Wrapper runtime does NOT read this file.** Wrapper only does file-existence checks (per Layer 2 hard rule).

### Layer 6 — Test seam (SessionExecutor interface)

`*SSHSession` is currently a concrete struct in `internal/shim/ssh.go`, so functions taking `*SSHSession` cannot be unit-tested with a stub against a temp `$HOME` directory. We introduce a small interface in `internal/shim/`:

```go
// SessionExecutor abstracts a remote SSH session for the purpose of
// install/uninstall/detect/recover testing. *SSHSession satisfies this
// interface; tests use a localSession stub that runs commands locally.
type SessionExecutor interface {
    // Exec runs a remote shell command and returns STDOUT only.
    // Stderr is intentionally discarded to match the existing semantics of
    // (*SSHSession).Exec at internal/shim/ssh.go:99 — SSH multiplex chatter
    // (control socket noise, etc.) lands on stderr and would otherwise
    // pollute callers that grep the output for content markers.
    Exec(cmd string) (stdout string, err error)

    // ExecWithStdin runs a remote shell command with the provided stdin and
    // returns COMBINED stdout+stderr. Combined output is appropriate here
    // because this method is used during install/uninstall where stderr
    // diagnostics (e.g. "mv: cannot create regular file: Permission denied")
    // are required for actionable error messages.
    ExecWithStdin(cmd string, stdin io.Reader) (combinedOutput string, err error)
}
```

Test stubs MUST honor these distinct semantics: a `localSession.Exec` stub that returns combined output by accident would diverge from production behavior and let regressions slip past unit tests.

Changes required to honor this seam:

- Add a new method `(*SSHSession).ExecWithStdin(cmd string, stdin io.Reader) (string, error)` that wraps the existing inline `exec.Command("ssh", ...)+Stdin` pattern (currently duplicated in `InstallRemoteClaudeWrapper` and `WriteRemoteState`).
- New functions `InstallRemoteClaudeWrapper`, `UninstallRemoteClaudeWrapper`, `DetectV070Corruption`, `RecoverV070Corruption` accept `SessionExecutor` instead of `*SSHSession`.
- Existing call sites in `cmd/cc-clip/main.go` keep passing `*SSHSession`, which satisfies the interface — no source-level break for callers.
- Tests use a `localSession` stub in `internal/shim/local_session_test.go` (or similar) that implements the interface by running commands via `bash -c` against a `t.TempDir()`-backed fake `$HOME`.

This is the test-seam compromise: minimal interface, narrow scope, only the four new install-side functions take the interface; the rest of the package keeps using `*SSHSession` directly. We do NOT introduce a wide refactor in v0.7.1.

---

## 3. Files modified

| File | Change |
|---|---|
| `internal/shim/claude_wrapper.go` | Wrapper template: add sidecar-first branch ahead of PATH-discovery. |
| `internal/shim/ssh.go` | Rewrite `InstallRemoteClaudeWrapper` (state-classify + mv-only + tmp-write+mv). New: `UninstallRemoteClaudeWrapper`, `DetectV070Corruption`, `RecoverV070Corruption`. |
| `internal/shim/deploy.go` | Add `ClaudeWrapperState` and `ClaudeWrapper` field on `DeployState`. |
| `cmd/cc-clip/main.go` | Add `--auto-recover` flag to `setup` + `connect`. Reject `--auto-recover --token-only` combination at flag-parse time (before any SSH session setup or network call) per §2 Layer 3 mutual-exclusion rule; exit non-zero with the diagnostic message defined there. Insert new pre-deploy step **N0** between line 558 (`fmt.Println("      SSH master connected")`) and line 561 (the `if tokenOnly` branch). This placement guarantees N0 runs on BOTH the full-deploy path AND the `--token-only` path: on `--token-only`, a corrupted remote still aborts because the user's claude is broken and a token sync would mask that. Action on detect: `corrupted` && no `--auto-recover` → print diagnostic and abort non-zero before any remote write (no binary upload at ~line 588+, no shim install at ~line 660, no token sync at line 567, no wrapper install at line 813). `corrupted` && `--auto-recover` → call `RecoverV070Corruption`, then proceed into the existing path. Existing `InstallRemoteClaudeWrapper` call at line 813 is unchanged in caller (the function itself is rewritten per Layer 1). Modify `cmdUninstall` (~line 399-403) to make local shim uninstall best-effort (warn-only) when `--host` is set; in the `--host` branch (~line 405-412), call `UninstallRemoteClaudeWrapper` BEFORE the existing PATH-marker cleanup. Update `printUsage` (~line 99-152) to document `--auto-recover` flag and the new wrapper-restore semantics of `uninstall --host`. |
| `internal/shim/ssh_test.go` (or new `claude_wrapper_install_test.go`) | All test scenarios in §4. |
| `docs/commands.md` | Add `--auto-recover` flag to the `setup` and `connect` command tables. Add a "Symlink-safe install" note explaining the `.cc-clip-real` sidecar artifact. Update `uninstall --host` row to mention wrapper restore. |
| `README.md` | Update the command-reference snippets (~line 24, 133, 149-162) and the `setup` vs `connect` section (~line 232) only if they currently document install/uninstall semantics that change. Specifically: add a one-line note that `--auto-recover` exists for users hitting the v0.7.0 corruption recovery path. Do NOT expand the README into full v0.7.1 release notes; keep that in the GitHub release body. |

No changes to: `internal/shim/hook_template.go`, `internal/shim/pathfix.go`, `internal/shim/connect.go`, daemon/notify code paths, `docs/notifications.md`, `docs/release.md`.

---

## 4. Test matrix

All tests run against a temp HOME directory simulating remote `~`. Use real `mv`, `ln -s`, `head` etc. on the local filesystem; mock the SSH session to execute commands locally.

| # | Scenario | Setup | Action | Expectation |
|---|---|---|---|---|
| 1 | Native installer layout, fresh install | `~/.local/bin/claude → versions/X.Y.Z` (symlink to fake 5KB ELF) | `InstallRemoteClaudeWrapper` | symlink moved to `.cc-clip-real` (still symlink, target unchanged); `claude` is regular file = wrapper script; fake ELF in versions store untouched |
| 2 | Regular file layout, fresh install | `~/.local/bin/claude` is a regular file (fake ELF) | `InstallRemoteClaudeWrapper` | file moved to `.cc-clip-real` (regular file); `claude` is wrapper |
| 3 | No prior install | `~/.local/bin/claude` does not exist | `InstallRemoteClaudeWrapper` | wrapper written; no sidecar created |
| 4 | Re-install (already cc-clip wrapper) | wrapper at `claude`, sidecar exists | `InstallRemoteClaudeWrapper` | wrapper rewritten via tmp+mv; sidecar untouched |
| 5 | Wrapper exec via sidecar | install scenario 1 + place fake `claude` shell script at sidecar target that `echo`s a token | run wrapper with no tunnel | wrapper exec's sidecar, token observed on stdout |
| 6 | Wrapper PATH-fallback (no sidecar, legacy install) | wrapper at `claude`, no sidecar, fake claude on `$PATH` outside self_dir | run wrapper | wrapper falls back to PATH-discovery, fake claude executed |
| 7 | Uninstall - symlink origin | install scenario 1, then uninstall | `UninstallRemoteClaudeWrapper` | wrapper deleted; `.cc-clip-real` mv'd back to `claude`; symlink restored, target unchanged |
| 8 | Uninstall - regular file origin | install scenario 2, then uninstall | `UninstallRemoteClaudeWrapper` | wrapper deleted; `.cc-clip-real` mv'd back as regular file |
| 9 | Uninstall refuses foreign file | place a non-cc-clip script at `claude` | `UninstallRemoteClaudeWrapper` | refused with warning; foreign file untouched |
| 10 | v0.7.0 corruption detected, no flag | construct exact corrupted state (symlink → wrapper, `.cc-clip-bak` is 5MB fake ELF) | `setup` (no flag) | `DetectV070Corruption` returns true; setup aborts non-zero with full diagnostic; no wrapper write occurs; corrupted state untouched |
| 11 | v0.7.0 corruption + `--auto-recover` | same as 10 | `setup --auto-recover` | backup mv'd back to versions target; symlink mv'd to `.cc-clip-real`; new wrapper installed; exec chain works |
| 12 | Corruption-like but backup missing | symlink + target is wrapper, no `.cc-clip-bak` | `setup --auto-recover` | C3 fails; abort with "backup absent — reinstall Claude Code" diagnostic; nothing modified |
| 13 | Corruption-like but backup is itself a wrapper | symlink + target is wrapper + `.cc-clip-bak` is also a wrapper (double-install pathology) | `setup --auto-recover` | C5 fails; abort with diagnostic; nothing modified |
| 14 | False-positive guard | regular file install + `.cc-clip-bak` happens to exist (large file from unrelated reason) | `setup` | C1 fails (not symlink) → `DetectV070Corruption` returns false → install proceeds normally |
| 15 | Symlink target is unrelated file (not wrapper) | `~/.local/bin/claude → some-other-binary` | `setup` | C2 fails → no corruption flag → standard install (mv symlink to sidecar) |
| 16 | Install rollback - regular file origin | regular file at `claude`; inject failure of the final `mv tmp → claude` step (e.g. delete `~/.local/bin` between staging mv and final mv via `localSession` hook) | `InstallRemoteClaudeWrapper` | error returned; sidecar `.cc-clip-real` was mv'd back to `claude` (best-effort rollback); tmp file removed; original regular file content unchanged |
| 17 | mktemp pre-existing-symlink guard | place `~/.local/bin/.claude.cc-clip-tmp.AAAAAA` as a symlink to a sensitive file; force `mktemp`'s random suffix to AAAAAA via test seam | `InstallRemoteClaudeWrapper` | mktemp returns failure (or generates a different suffix); the planted symlink target is NOT written to; install either retries with a different suffix or aborts cleanly |
| 18 | Sidecar collision - regular file already at sidecar path | symlink at `claude` + a stale regular file at `claude.cc-clip-real` (e.g. from a half-completed prior install) | `InstallRemoteClaudeWrapper` | install aborts pre-stage with diagnostic suggesting `rm ~/.local/bin/claude.cc-clip-real`; symlink at `claude` is untouched; tmp removed; user's claude still works |
| 19 | Sidecar collision - directory at sidecar path | regular file at `claude` + a directory at `claude.cc-clip-real` (the silent-corruption footgun case) | `InstallRemoteClaudeWrapper` | install aborts pre-stage with diagnostic; verify origin was NOT moved into the directory (that would be the bug we're guarding against); user's claude still works |
| 20 | N0 detection on `--token-only` path | constructed v0.7.0 corrupted remote | `cc-clip connect <host> --token-only` (no `--auto-recover`) | N0 detection fires; setup aborts non-zero with diagnostic; token sync at line 567 NEVER runs; remote token file unchanged |
| 21 | Test stub Exec/ExecWithStdin semantics | localSession stub returns "out\n" on stdout and "err\n" on stderr for `echo out; echo err >&2` | call stub `Exec` and `ExecWithStdin` | `Exec` returns `"out\n"` only (stderr discarded). `ExecWithStdin` returns `"out\nerr\n"` (combined, order may vary by buffering). This locks the contract that production and stub stay aligned — the wrong stub semantics are exactly what would let MEDIUM 1 regress silently. |
| 22 | Flag combination `--auto-recover --token-only` is rejected | (no remote needed) | `cc-clip setup <host> --auto-recover --token-only` | exit non-zero at flag-parse time, before SSH session setup; stderr contains the conflict-resolution message from §2 Layer 3; verify NO `NewSSHSession` call was made (use a test seam or a host that would fail to resolve to confirm we exit before networking) |

Test helpers needed:
- `t.TempDir()` based fake `$HOME` with `.local/bin/` and `.local/share/claude/versions/X.Y.Z/`.
- `localSession` stub implementing the `SessionExecutor` interface (per §2 Layer 6), running commands via `bash -c` against the temp HOME. Critically: bash interprets `~` against `$HOME`, which the stub sets to `t.TempDir()`. Tests do NOT call the real `*SSHSession` constructor.
- Fixture: a ≥5MB random file to play "real claude binary" (so C4 size threshold triggers naturally); the wrapper script content is the real `ClaudeWrapperScript(port)` output (so C2/C5 marker matching uses real bytes, not a hand-crafted near-miss).

---

## 5. Verification

After implementation, before any commit/PR:

```bash
go test ./internal/shim -count=1 -race
go test ./cmd/cc-clip ./internal/shim -count=1 -race
go vet ./...
make build
```

Before tagging v0.7.1:

```bash
make release-preflight   # if defined; otherwise the existing release-local + smoke check
```

Manual smoke (real remote):

1. Set up a fresh Ubuntu 22.04 VM with Anthropic's Native Installer (`curl https://claude.ai/install.sh`).
2. Run `cc-clip setup <host>` — verify symlink intact, real binary intact, `claude --version` works.
3. SSH in, run `cc-clip-hook` smoke (existing notification probe in step N6 should still pass).
4. Run `cc-clip uninstall --host <host>` — verify symlink restored to original target, no leftover `.cc-clip-real` (a `.cc-clip-bak` from a prior v0.7.0 install is preserved with an info message; new installs do not create one), real binary intact.
5. Construct v0.7.0 corrupted state by hand on a second VM. Run `cc-clip setup <host>` → expect abort with diagnostic and zero remote writes. Run `cc-clip setup <host> --auto-recover` → expect clean install + working `claude`.

---

## 6. Release notes (v0.7.1 draft)

```markdown
## v0.7.1 — Fix: claude wrapper no longer corrupts the real binary on Native Installer layouts

### Bug fix
- `cc-clip setup` / `cc-clip connect` previously followed the symlink at
  `~/.local/bin/claude` and overwrote the actual claude binary inside
  `~/.local/share/claude/versions/X.Y.Z`. This affected every user installing
  Claude Code via Anthropic's official Native Installer (`curl claude.ai/install.sh`).
- The install path is now symlink-safe: the original entry (regular file or
  symlink) is renamed to `~/.local/bin/claude.cc-clip-real`, and the wrapper is
  written via tmp-file + atomic rename. The real claude binary is never touched.

### Recovery for existing v0.7.0 victims
On v0.7.1, running `cc-clip setup <host>` against a remote whose claude binary
was corrupted by v0.7.0 will:

1. Detect the corruption (five-condition check) and abort with a clear diagnostic.
2. Show a copy-paste recovery command.
3. Offer `cc-clip setup <host> --auto-recover` to perform recovery + reinstall in one step.

If you prefer to recover manually:

    ssh <host>
    mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"

Then re-run `cc-clip setup <host>` from your local machine.

If `~/.local/bin/claude.cc-clip-bak` is missing on your remote, your real claude
binary cannot be recovered automatically. Reinstall Claude Code via
`curl https://claude.ai/install.sh` and then re-run `cc-clip setup`.

### New: `cc-clip uninstall --host <host>` cleans up the claude wrapper
The existing `cc-clip uninstall --host <host>` command now also removes the
cc-clip claude wrapper from `~/.local/bin/claude` and restores the original
file or symlink from the `~/.local/bin/claude.cc-clip-real` sidecar created
during install. If the file at `~/.local/bin/claude` is not a cc-clip wrapper
(e.g. user replaced it manually), it is left untouched and the command emits
a warning.
```

---

## 7. Decisions log (for reviewer reference)

| Decision | Choice | Rationale |
|---|---|---|
| Approach (A/B/C) | **B**: rename symlink + wrapper holds explicit sidecar | A leaves wrapper unable to find real claude under Native Installer layout; C downgrades semantics to interactive-shell-only |
| Sidecar naming | `~/.local/bin/claude.cc-clip-real` (single name for all origin kinds) | Wrapper logic uniform; `.cc-clip-bak` reserved as a v0.7.0 historical artifact name we recognize but don't produce |
| Write technique | tmp file + `chmod +x` + `mv` to final path | Atomic; guarantees we never `>` into a possibly-symlink path even defensively |
| State storage | `DeployState.ClaudeWrapper` JSON sub-struct | Used for diagnostics/uninstall hints; **wrapper runtime does not parse JSON** |
| v0.7.0 recovery default | **Fail closed** (abort + diagnostic) | A 250MB user binary move is not in the implicit authorization scope of `cc-clip setup`. Detection precision ≠ default-write authorization |
| `--auto-recover` flag | Opt-in single-step recovery + reinstall | Same code path as default detection; just changes whether we proceed |
| Recovery sub-command (`cc-clip recover-claude`) | **Not introduced** | Discovery rate too low; v0.7.1 victims will re-run `setup`, which is where the fix must surface |
| Pre-existing `uninstall --codex` flow | Untouched | Out of scope; only the no-flag uninstall gains wrapper restoration |

---

## 8. Out of scope

- Doctor / diagnostic sub-command. The `DeployState.ClaudeWrapper` field will support a future `cc-clip doctor` command but no command is added in v0.7.1.
- Anthropic Claude Code self-update conflict handling. After Anthropic's installer overwrites `~/.local/bin/claude` during an upgrade, the user must re-run `cc-clip setup`. This is documented behavior and not a regression.
- Behavior on platforms other than Linux remote. macOS-as-remote and Windows-as-remote are unchanged; the wrapper-on-remote concept is Linux-only by design.
