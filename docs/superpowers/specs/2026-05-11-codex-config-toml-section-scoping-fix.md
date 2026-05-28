# Codex `config.toml` section-scoping fix

**Status**: Draft, awaiting separate issue creation
**Date**: 2026-05-11
**Author**: Claude Code session (with shunmei review)
**Out of scope for**: issue #55 / branch `fix/issue-55-claude-wrapper-symlink-safe`
**In scope for**: a new GitHub issue to be filed; a new branch to be cut from `main`

---

## Symptom

After running `cc-clip connect <host> --codex --force` against a remote
that already has a non-trivial `~/.codex/config.toml`, the remote `codex`
CLI becomes unusable. Concretely:

- `codex login` fails to start (cannot read config to determine auth
  provider / endpoints).
- All other `codex` subcommands fail similarly because Codex parses
  `config.toml` first, on every invocation.
- The user-visible message is something like
  "无法登陆" / "cannot log in", which is the symptom — not the root cause.

`~/.codex/auth.toml` is **not** touched by cc-clip; the perceived
"auth.toml is broken" reading was a misattribution. The destruction is
limited to `~/.codex/config.toml`.

## Root cause

Located in `internal/shim/ssh.go:398-437` (`EnsureRemoteCodexNotifyConfig`).
The function writes the cc-clip notify hook to `config.toml` by
**appending** a managed block to the end of the file:

```bash
mkdir -p ~/.codex && cat >> ~/.codex/config.toml << 'CC_CLIP_EOF'
# >>> cc-clip notify (do not edit) >>>
notify = ["cc-clip", "notify", "--from-codex-stdin"]
# <<< cc-clip notify (do not edit) <<<
CC_CLIP_EOF
```

This triggers three independent failure modes, any of which can break
parsing:

### F1 — TOML section scoping (primary cause)

In TOML, every bare `key = value` line belongs to the **most recent
preceding `[section]` header**. There is no notion of "top-level after
a section starts".

If the user's `config.toml` has any section (very common — `[history]`,
`[mcp_servers.xyz]`, `[model_providers.openai]`), appending our
top-level `notify = [...]` at the end reparents it under that section:

```toml
[model_providers.openai]
api_key = "..."

# >>> cc-clip notify (do not edit) >>>
notify = ["cc-clip", "notify", "--from-codex-stdin"]
# <<< cc-clip notify (do not edit) <<<
```

Codex parses this as `model_providers.openai.notify = [...]`. New Codex
builds use Rust `serde` deserialization with `#[serde(deny_unknown_fields)]`
on the provider structs, so this triggers an immediate deserialization
error. The error short-circuits `codex` startup, so even unrelated
subcommands like `codex login` cannot proceed.

### F2 — Trailing-newline corruption

If the existing `config.toml` does not end with a newline (vim users
often have this), `cat >>` concatenates the last line with our marker:

```toml
model = "gpt-5"# >>> cc-clip notify (do not edit) >>>
```

That is a hard TOML syntax error.

### F3 — Non-atomic write

The append happens via the shell's `>>` redirection. If SSH is
interrupted mid-write (network blip, user Ctrl-C), the file is left
with a partial managed block — neither the old config nor the new one.
This is rare but unrecoverable without manual editing.

### Why `--force` makes it worse

`runConnect` calls `connectNotifySetup` unconditionally, which calls
`EnsureRemoteCodexNotifyConfig` whenever `RemoteHasCodex` returns true.
On `--force`, the function:

1. sees an existing managed block,
2. sed-deletes it,
3. appends a fresh block at the end again.

Because step 3 still appends at the very end, every `--force` repeats
F1: the managed block keeps landing inside whatever section happens to
be last in the file. There is no idempotent escape — repeating the
command does not recover.

## Fix design

Two structural changes plus one safety guard.

### Change 1 — Prepend, don't append

The fix requires `notify` to live **before** any `[section]` header, so
TOML interprets it as truly top-level. Concretely, prepend the managed
block to the very start of the file:

```toml
# >>> cc-clip notify (do not edit) >>>
notify = ["cc-clip", "notify", "--from-codex-stdin"]
# <<< cc-clip notify (do not edit) <<<

# ... user's original config follows ...
```

### Change 2 — Atomic mktemp + mv (same-filesystem)

Build the new file into a `mktemp` path **inside `~/.codex/` itself**,
then atomically `mv` it over `~/.codex/config.toml`. SSH interruption
either leaves the original file untouched or completes the swap —
never a half-written state.

**Critical**: `mktemp` defaults to `/tmp`, which on mainstream Linux is
a tmpfs mount distinct from `$HOME`. A `mv` across filesystems is NOT
a `rename(2)` — it degrades to `copy + unlink`, which is not atomic
and can leave a half-written destination if interrupted. The temp
file must live in the same directory (or at minimum the same mount
point) as the final path, so `mv` takes the `rename(2)` path:

```bash
tmp=$(mktemp "$HOME/.codex/.config.toml.cc-clip-tmp.XXXXXX")
```

The dotfile prefix keeps the temp invisible in `ls` and signals
"don't touch" to any tooling that scans the directory.

### Change 3 — User-notify guard, post-strip

Before writing, scan the *post-strip* content (i.e. with our managed
block removed) for a non-managed `notify =` line. If found, refuse to
write and surface a clear error. Currently the user-notify check runs
on raw input, which means it false-fires on idempotent re-injection
when our own block contains `notify =`.

### Proposed bash logic

```bash
set -e
mkdir -p ~/.codex
# Temp file MUST be on the same filesystem as ~/.codex/config.toml so
# `mv` takes the rename(2) path. Default mktemp uses /tmp, which is
# tmpfs on most Linux distros — that would silently degrade mv to a
# non-atomic copy+unlink and break crash safety.
tmp=$(mktemp "$HOME/.codex/.config.toml.cc-clip-tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT

# Strip any prior managed block; pipe original content (or empty) through sed
existing=""
if [ -f ~/.codex/config.toml ]; then
    existing=$(sed '/^# >>> cc-clip notify (do not edit) >>>/,/^# <<< cc-clip notify (do not edit) <<<$/d' ~/.codex/config.toml)
fi

# Refuse if user has a non-managed top-level notify key
if printf '%s\n' "$existing" | grep -E '^\s*notify\s*=' >/dev/null; then
    echo "existing notify key found; refusing to inject" >&2
    exit 7
fi

# Build new file: managed block first, then existing content
{
    printf '%s\n' '# >>> cc-clip notify (do not edit) >>>'
    printf '%s\n' 'notify = ["cc-clip", "notify", "--from-codex-stdin"]'
    printf '%s\n' '# <<< cc-clip notify (do not edit) <<<'
    printf '\n'
    printf '%s' "$existing"
    # Ensure trailing newline
    [ -n "$existing" ] && [ "${existing: -1}" != $'\n' ] && printf '\n'
} > "$tmp"

# Preserve original permissions if file existed
if [ -f ~/.codex/config.toml ]; then
    chmod --reference=~/.codex/config.toml "$tmp" 2>/dev/null || true
fi

mv "$tmp" ~/.codex/config.toml
trap - EXIT
```

### Companion fix — Uninstall path

`cmdUninstallCodexRemote` (cmd/cc-clip/main.go:433) does **not** clean
up the managed block in `config.toml`. After uninstall, the remote
still has a `notify = [...]` line pointing at a now-missing cc-clip
binary, so Codex tries to run a stale hook on every event. Add a
sed-strip pass during uninstall that removes the managed block while
preserving the rest of the file.

## Test plan

All tests use the existing `localSession` stub against a `t.TempDir()`
fake $HOME, mirroring the issue #55 test pattern:

1. **TestEnsureCodexNotifyConfig_PrependBeforeSection** — pre-existing
   config with `[model_providers.openai]` section; assert managed block
   lands BEFORE the section header, and a TOML parser (or `taplo`
   binary, or a Go TOML package as test-only dependency) accepts the
   result and `notify` deserializes at the root.
2. **TestEnsureCodexNotifyConfig_NoTrailingNewline** — file without
   trailing `\n`; assert no concatenation, valid TOML out.
3. **TestEnsureCodexNotifyConfig_IdempotentReinjection** — run twice
   in a row; assert only one managed block exists and content is
   byte-identical to single-run output.
4. **TestEnsureCodexNotifyConfig_UserHasNotify_Refuses** — pre-existing
   file with `notify = [...]` outside our markers; assert error, file
   unchanged.
5. **TestEnsureCodexNotifyConfig_EmptyFile** — empty config; assert
   managed block written, no extra blank lines.
6. **TestEnsureCodexNotifyConfig_FileDoesNotExist** — `~/.codex/`
   doesn't exist; assert directory + file created.
7. **TestEnsureCodexNotifyConfig_AtomicWriteOnFailure** — inject a
   simulated failure (e.g. read-only target dir via chmod); assert
   original file content unchanged (or no file created if it didn't
   exist before).
8. **TestCmdUninstallCodexRemote_StripsManagedBlock** — extends the
   uninstall integration test; assert post-uninstall `config.toml`
   contains no `cc-clip notify` markers and rest of user content
   preserved.

## Scope boundary

- This is a **separate issue from #55**. The branch must be cut from
  `main` after #55 is merged (or independently if #55 lingers).
- Do not piggyback this onto the existing `fix/issue-55-...` branch.
  The N0 fail-closed work belongs to #55's scope; codex config.toml
  belongs to its own narrative.
- Suggested branch name: `fix/codex-config-toml-section-scoping`.
- Suggested commit prefixes:
  - `feat(shim): prepend codex notify block atomically via mktemp+mv`
  - `feat(shim): strip codex notify block during uninstall`
  - `test(shim): codex notify config injection table coverage`

## Open questions for the new issue

1. Should we add a TOML linter as a CI gate to catch this class of bug
   structurally? `taplo` is the canonical Rust tool; pure-Go options
   include `pelletier/go-toml`.
2. Should `EnsureRemoteCodexNotifyConfig` accept an optional `[hooks]`
   section name in case future Codex versions move `notify` under a
   sub-table? (Probably YAGNI — wait until it actually moves.)
3. Should we detect the corrupted state on N5 and offer a recovery
   path, similar to N0 for the v0.7.0 wrapper issue? At minimum: detect
   that our managed block is positioned under a `[section]` and warn,
   even if we can't auto-fix older injections.
