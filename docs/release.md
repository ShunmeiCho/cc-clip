# Releasing cc-clip

> Looking for how to **install or upgrade** cc-clip as a user? See
> [upgrading.md](upgrading.md). This document is for maintainers cutting a
> new release.

Production releases are cut by pushing an annotated `v<semver>` tag to the `main`
branch. The `Release` workflow in `.github/workflows/release.yml` reacts to the
tag push, runs the test suite, validates the release contract, and invokes
GoReleaser to publish the archives and checksums.

**Do not** run `make release-local` plus `gh release create` by hand.
`make release-local` produces bare binaries named `cc-clip-<os>-<arch>`,
whereas `scripts/install.sh` expects GoReleaser's `cc-clip_<version>_<os>_<arch>.<ext>`
layout. Mixing the two silently breaks `install.sh` downloads.

## Phase 0: Decide the version

Follow semver against the user-visible surface (CLI flags, shim contract,
notification schema, public HTTP endpoints):

| Change | Bump |
|---|---|
| Breaking CLI / shim / env-var removal or rename | major |
| New user-facing feature (new subcommand, new client support) | minor |
| Bug fix, security hardening, compatibility fix | patch |

## Phase 1: Make sure `main` is ready

```bash
git checkout main
git pull --ff-only
git status                        # must be clean
git log v<previous>..HEAD --oneline   # confirm all intended PRs are in
```

## Phase 2: Pre-flight (reproduce every CI step locally)

Running this **before** tagging is the single most important step. The release
workflow has a `Verify release contract` grep block that is easy to fall out of
sync with `.goreleaser.yaml` or `scripts/install.sh`; catching that locally
costs nothing, catching it after pushing a tag wastes a version number.

```bash
make release-preflight
```

This target runs, in order:

1. `make test` — same `go test ./...` CI runs.
2. `go vet ./...`.
3. Cross-compile sanity for all six target triples.
4. `goreleaser check` — validates `.goreleaser.yaml` against the current
   GoReleaser schema.
5. The release-contract greps. These enforce that the three files below
   agree on the archive naming and asset layout, because if any one of
   them drifts alone, a real user-visible path breaks silently:
   - `.goreleaser.yaml` — what goreleaser actually emits
     (`name_template`, `formats: [tar.gz]`, `formats: [zip]`).
   - `scripts/install.sh` — what the install-script path expects to
     download.
   - `cmd/cc-clip/update.go` — `releaseArchiveName()` is the single
     source of truth for the `cc-clip update` command. The grep pins
     the format string and the `checksums.txt` fetch, so any future
     change to asset naming forces a matching update in the updater.
6. `goreleaser release --snapshot --clean --skip=publish` — actually builds
   every archive to `dist/` without publishing, so archive-time issues
   surface locally.

Stop and fix on the first failure.

If a grep fails, fix the drift in the file it names AND in every other
file in the list above. The contract is a three-way agreement, not
two separate pairs.

## Phase 3: Cut and push the tag

```bash
V=v0.6.2   # adjust

# Reject accidental retag of an existing remote version
if git ls-remote --tags origin "$V" | grep -q "refs/tags/$V$"; then
  echo "ERROR: $V already on origin" >&2
  exit 1
fi

# Remove any local leftover from a previous attempt
git tag -d "$V" 2>/dev/null || true

# Annotated tag with human-readable release summary.
# GoReleaser auto-generates the GitHub release notes from commits, but this
# tag body is what shows up in `git show $V` forever.
git tag -a "$V" -F - <<EOF
$V — <one-line subject>

<grouped bullets of what users get, e.g.:>

Security:
- ...

Features:
- ...

Fixes:
- ...
EOF

# Confirm the tag points at HEAD (the commit you just validated in Phase 2)
test "$(git rev-parse $V^{commit})" = "$(git rev-parse HEAD)" || {
  echo "ERROR: tag is not at HEAD" >&2
  exit 1
}

git push origin "$V"
```

## Phase 4: Watch CI

`gh run list --limit 1` picks "whatever the newest release workflow run is",
which is a race: the tag push takes a few seconds to create a new run, and
in that window you get the previous release's run id. Filter by the commit
the tag points at, and poll until a matching run exists.

```bash
V=v0.6.2                                  # same tag as Phase 3
COMMIT=$(git rev-parse "$V^{commit}")
RUN_ID=""
for attempt in $(seq 1 30); do
  RUN_ID=$(gh run list \
    --workflow=release.yml --event push --commit "$COMMIT" \
    --limit 1 --json databaseId --jq '.[0].databaseId // empty')
  [ -n "$RUN_ID" ] && break
  sleep 2
done
[ -z "$RUN_ID" ] && {
  echo "ERROR: no release workflow run appeared for $COMMIT within 60s" >&2
  exit 1
}

gh run watch "$RUN_ID" --exit-status
```

Non-zero exit means the release was not published. See **Phase 6** for recovery.

## Phase 4.5: Write real release notes

GoReleaser's default release body is a two-line commit list with SHAs,
which is the minimum the release page can legally show. For every tag
worth cutting, replace it with a user-facing summary before moving on.
The tag annotation from Phase 3 is `git show $V` forever, but it is NOT
what end users see on GitHub until this step happens.

```bash
V=v0.6.2
gh release edit "$V" --notes-file path/to/notes.md
```

The notes file should cover, in this order:

1. **What's New** — the flagship change, with a short code block if it
   introduces a new command or flag.
2. **Other Improvements** — documentation, tooling, CI, everything else
   that users might notice but is not the headline.
3. **Upgrade** — concrete commands per platform. On cc-clip this is
   `cc-clip update` for 0.6.2+ users, `install.sh` for fresh installs
   and Windows-to-manual, and always the `cc-clip connect <host> --force`
   reminder for remote hosts.
4. **Not in This Release** — features deferred, known limitations, links
   to issues tracking follow-ups. This stops users filing "why didn't
   X ship" questions.
5. **Verification** — a short snippet showing how to check the archive
   against `checksums.txt`. Makes integrity verification a copy-paste
   one-liner.
6. **Full Commit List + Diff link** — short SHAs, link to
   `https://github.com/<repo>/compare/<prev>...<tag>`.

Compare the end result against the release pages for the previous two
versions — the shape should match so users subscribed to releases can
skim instead of re-reading the format each time.

## Phase 5: Verify the published release

Never skip this — CI passing is necessary but not sufficient. The artifacts
need to actually install cleanly.

```bash
V=v0.6.2
Vnum=${V#v}

# 1. Assets present
gh release view "$V" --json assets --jq '.assets[].name'
# Expected: 4 tar.gz (darwin/linux × amd64/arm64) + 2 zip (windows × amd64/arm64) + checksums.txt

# 2. /releases/latest points at this version (install.sh resolves via latest)
LATEST=$(curl -sL -o /dev/null -w "%{url_effective}" \
  https://github.com/ShunmeiCho/cc-clip/releases/latest)
case "$LATEST" in
  */tag/$V) echo "latest=$V ok" ;;
  *) echo "ERROR: latest=$LATEST" >&2; exit 1 ;;
esac

# 3. Download + checksum + execute one archive end-to-end
TMP=$(mktemp -d)
gh release download "$V" --repo ShunmeiCho/cc-clip \
  --pattern "cc-clip_${Vnum}_darwin_arm64.tar.gz" \
  --pattern "checksums.txt" \
  --dir "$TMP"
(cd "$TMP" && shasum -a 256 -c checksums.txt --ignore-missing)
tar -xzf "$TMP/cc-clip_${Vnum}_darwin_arm64.tar.gz" -C "$TMP"
"$TMP/cc-clip" --version   # Must print "cc-clip $Vnum"
rm -rf "$TMP"
```

## Phase 6: Recovering from failures

### CI failed before publishing (tests, contract, goreleaser)

The tag was pushed but no release was created.

Do **not** force-push the tag. Instead:

```bash
git push origin --delete "$V"   # remove bad remote tag
git tag -d "$V"                 # remove local tag
# open a PR fixing the root cause, merge it, rerun Phase 2, then retag from new HEAD
```

### Release was published but has a serious bug

Tags are immutable by convention — do not delete published tags.
Mark the release as non-latest so `install.sh` stops handing it out, then cut a
patch:

```bash
gh release edit "$V" --prerelease
# fix on main, tag v<next patch>, release again
```

### Phase 5 verification fails (checksums mismatch, binary fails to run)

Treat this as compromised and immediately quarantine:

```bash
gh release edit "$V" --prerelease
```

Then investigate GoReleaser logs (`gh run view $RUN_ID --log`) for the build
step that produced the broken archive.

## Appendix: Why Phase 2 exists

Every new trap caught at release time in this project's history has been
either (a) a drift between `.github/workflows/release.yml` contract checks and
`.goreleaser.yaml`, or (b) a build-time issue on an arch the developer does
not normally compile for. Both classes are cheap to catch locally and expensive
to catch after pushing a tag. `make release-preflight` is the one-shot that
mirrors CI exactly, so the tag push becomes a ceremony rather than a gamble.

### Appendix: the release contract is three-way

The asset layout a cc-clip release ships is a shared contract across
three files that MUST stay aligned:

| File | Role | What it encodes |
|---|---|---|
| `.goreleaser.yaml` | emits | `name_template`, per-arch `formats` |
| `scripts/install.sh` | consumes (new install) | `ARCHIVE_NAME` = `cc-clip_${VERSION#v}_${PLATFORM}.tar.gz` |
| `cmd/cc-clip/update.go` | consumes (self-upgrade) | `releaseArchiveName()` format string + `checksums.txt` fetch |

If any one of these changes alone, real users break silently:

- drift in `.goreleaser.yaml` -> published archive filename no longer
  matches what the downstream consumers expect.
- drift in `install.sh` -> new installs 404 on download URL.
- drift in `update.go` -> existing users on the previous release
  cannot self-upgrade and get a 404 from `cc-clip update`.

`make release-preflight` greps all three in the same step precisely so
that a single-sided change trips the gate before it ships. If you ever
find yourself changing asset naming, change all three in the same PR
and verify `make release-preflight` still passes.
