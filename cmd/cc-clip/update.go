package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/service"
)

const (
	updateRepo          = "ShunmeiCho/cc-clip"
	updateAPIURL        = "https://api.github.com/repos/" + updateRepo + "/releases"
	updateDownloadBase  = "https://github.com/" + updateRepo + "/releases/download"
	updateDaemonPort    = 18339
	updateDownloadTotal = 5 * time.Minute
)

type updateOptions struct {
	check bool
	force bool
	toVer string
}

func cmdUpdate() {
	opts := parseUpdateFlags(os.Args[2:])

	if runtime.GOOS == "windows" {
		log.Fatal("cc-clip update is not supported on Windows yet. See docs/upgrading.md for the manual upgrade steps.")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		log.Fatalf("cc-clip update does not support %s yet; please upgrade manually via install.sh or the release archives.", runtime.GOOS)
	}

	selfPath, err := resolveSelfPath()
	if err != nil {
		log.Fatalf("cannot determine current cc-clip binary path: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTotal)
	defer cancel()

	currentTag := normalizeVersion(version)
	target := strings.TrimSpace(opts.toVer)
	if target != "" {
		target = normalizeVersion(target)
	} else {
		latest, err := fetchLatestReleaseTag(ctx)
		if err != nil {
			log.Fatalf("failed to query latest release: %v", err)
		}
		target = latest
	}
	if target == "" {
		log.Fatal("could not determine a target version to install.")
	}

	fmt.Printf("current:  %s\n", displayVersion(currentTag))
	fmt.Printf("target:   %s\n", target)
	fmt.Printf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("binary:   %s\n", selfPath)

	sameVer := currentTag != "" && currentTag == target
	if opts.check {
		if sameVer {
			fmt.Println("already at target version; nothing to do.")
		} else {
			fmt.Printf("update available: %s -> %s\n", displayVersion(currentTag), target)
		}
		return
	}
	if sameVer && !opts.force {
		fmt.Println("already at target version; nothing to do. Use --force to re-install.")
		return
	}

	// Surface conflicts BEFORE we download anything. This is the biggest
	// real-world upgrade trap: an unrelated bundled copy of cc-clip (or a
	// forgotten stray daemon) holds port 18339, so service install's new
	// plist crash-loops, `cc-clip connect` still sees "a daemon" and syncs
	// the wrong token to remotes.
	if conflict, err := detectDaemonConflict(updateDaemonPort, selfPath); err != nil {
		fmt.Printf("warning: could not detect port conflicts: %v\n", err)
	} else if conflict != "" {
		if !opts.force {
			log.Fatalf("conflict on :%d — %s\nresolve it before updating (stop / unload the other daemon), or rerun with --force.", updateDaemonPort, conflict)
		}
		fmt.Printf("warning: ignoring conflict because --force was given: %s\n", conflict)
	}

	// Snapshot whether a service was running BEFORE any mutation. We need
	// this later both to know whether to call `service install` after the
	// swap AND to tell the rollback path whether to re-register the plist
	// against the restored binary. Reading Status() mid-flight is not
	// safe because the service may crash-loop during `service.Uninstall()`
	// and look "not running" when in fact we need to restore it.
	serviceWasRunning := detectServiceRunning()
	fmt.Printf("service:  %s\n", describeServiceState(serviceWasRunning))

	archivePath, cleanupArchive, err := downloadAndVerifyArchive(ctx, target)
	if err != nil {
		log.Fatalf("download/verify failed: %v", err)
	}
	defer cleanupArchive()

	stagedBinary, cleanupStaged, err := extractBinary(archivePath)
	if err != nil {
		log.Fatalf("extract failed: %v", err)
	}
	defer cleanupStaged()

	if runtime.GOOS == "darwin" {
		if err := prepareMacOSBinary(stagedBinary); err != nil {
			log.Fatalf("macOS signing prep failed: %v", err)
		}
	}

	// Verify the staged binary BEFORE touching the installed binary. If the
	// archive is corrupt or contains the wrong version, we abort cleanly with
	// nothing installed. This matches a "trust but verify" contract: the
	// checksum file says the archive matches a published asset, but nothing
	// stops a future release-pipeline bug from publishing a mislabeled file.
	stagedVersion, err := runVersionCommand(stagedBinary)
	if err != nil {
		log.Fatalf("staged binary from archive failed to run --version: %v\nthe archive may be corrupt for this platform (%s/%s)", err, runtime.GOOS, runtime.GOARCH)
	}
	if !stagedVersionMatches(stagedVersion, target) {
		log.Fatalf("staged binary reports %q but target is %s; refusing to install (archive mislabeled?)\nnothing has been changed on disk.", stagedVersion, target)
	}
	fmt.Printf("staged binary verified: %s\n", stagedVersion)

	backup := selfPath + ".bak"
	if err := os.Rename(selfPath, backup); err != nil {
		log.Fatalf("failed to back up current binary: %v", err)
	}
	restoreBinary := func() {
		_ = os.Remove(selfPath)
		_ = os.Rename(backup, selfPath)
	}

	if err := renameAtomic(stagedBinary, selfPath); err != nil {
		restoreBinary()
		log.Fatalf("failed to install new binary (rolled back): %v", err)
	}
	if err := os.Chmod(selfPath, 0o755); err != nil {
		restoreBinary()
		log.Fatalf("failed to chmod new binary (rolled back): %v", err)
	}

	fmt.Printf("binary installed: %s\n", selfPath)

	// If a service WAS running before the swap, re-register the plist so
	// launchd reloads the job pointing at the new binary. Failure here is
	// treated the same as any other post-install failure: roll back before
	// returning.
	if err := reinstallMacOSService(selfPath, serviceWasRunning); err != nil {
		rollbackAfterInstall(selfPath, backup, serviceWasRunning)
		log.Fatalf("service restart failed (%v); rolled back to previous version.", err)
	}

	// Post-install re-verify 1: does the new binary on disk actually run,
	// and does it report the expected version? This catches an archive
	// that staged correctly but then couldn't exec (missing codesign on
	// macOS, broken linked library, etc.).
	installedVersion, verifyErr := runVersionCommand(selfPath)
	if verifyErr != nil {
		rollbackAfterInstall(selfPath, backup, serviceWasRunning)
		log.Fatalf("post-install: installed binary failed to run (%v); rolled back to previous version.\ncheck %s for the archive you were trying to install.", verifyErr, backup)
	}
	if !stagedVersionMatches(installedVersion, target) {
		rollbackAfterInstall(selfPath, backup, serviceWasRunning)
		log.Fatalf("post-install: installed binary reports %q, expected %s; rolled back.", installedVersion, target)
	}
	fmt.Printf("verified binary: %s\n", installedVersion)

	// Post-install re-verify 2: if a service was running before the update,
	// it should be running AGAIN now, owned by the new binary. This catches
	// the bad-state where launchd registered the new plist but the old
	// (third-party) daemon is still holding the port — which is exactly
	// what happens after `--force` past a conflict, or if `service install`
	// silently succeeded but launchd's job crash-looped.
	if serviceWasRunning {
		if err := verifyRunningDaemon(selfPath, updateDaemonPort); err != nil {
			rollbackAfterInstall(selfPath, backup, serviceWasRunning)
			log.Fatalf("post-install: daemon verification failed (%v); rolled back to previous version.\nresolve the stray process on :%d (see `docs/upgrading.md`) and try again.", err, updateDaemonPort)
		}
		fmt.Println("verified daemon: responding on :" + strconv.Itoa(updateDaemonPort) + " from the new binary")
	}

	_ = os.Remove(backup)

	printPostUpdateReminders(target, selfPath, serviceWasRunning)
}

// stagedVersionMatches compares `cc-clip --version` output (e.g. "cc-clip 0.6.1")
// with a release tag (e.g. "v0.6.1"). The ldflags version baked into the
// binary carries no "v" prefix, so we normalize both sides before comparing.
func stagedVersionMatches(reported, targetTag string) bool {
	r := strings.TrimSpace(reported)
	r = strings.TrimPrefix(r, "cc-clip ")
	r = strings.TrimPrefix(r, "v")
	t := strings.TrimPrefix(strings.TrimSpace(targetTag), "v")
	return r == t && r != ""
}

// detectServiceRunning snapshots whether a cc-clip service is running now.
// We read this BEFORE any mutation because once `service.Uninstall()` runs
// the state is destroyed and we cannot tell from the outside whether the
// service was supposed to exist or not.
func detectServiceRunning() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	running, err := service.Status()
	return err == nil && running
}

func describeServiceState(wasRunning bool) string {
	if runtime.GOOS != "darwin" {
		return "not managed (this OS does not use launchd)"
	}
	if wasRunning {
		return "running (launchd)"
	}
	return "not running"
}

// reinstallMacOSService re-registers the launchd plist against binaryPath,
// but only if a service was running before the update. Returns a non-nil
// error so the caller can rollback; the function does not panic or exit.
func reinstallMacOSService(binaryPath string, serviceWasRunning bool) error {
	if runtime.GOOS != "darwin" || !serviceWasRunning {
		return nil
	}
	if err := service.Uninstall(); err != nil {
		return fmt.Errorf("service uninstall: %w", err)
	}
	if err := service.Install(binaryPath, updateDaemonPort); err != nil {
		return fmt.Errorf("service install: %w", err)
	}
	return nil
}

// verifyRunningDaemon polls /health until 200 OK, then confirms that the
// process listening on `port` is actually the binary at expectedPath. This
// catches the scenario where launchd accepted the new plist but the old
// third-party daemon is still bound to the port (common after `--force`
// past a conflict check).
func verifyRunningDaemon(expectedPath string, port int) error {
	healthCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)

	var lastErr error
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(healthCtx, "GET", healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
			lastErr = fmt.Errorf("GET /health -> %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-healthCtx.Done():
			return fmt.Errorf("daemon did not respond 200 to /health within 10s (last: %v)", lastErr)
		case <-time.After(500 * time.Millisecond):
		}
	}

	conflict, err := detectDaemonConflict(port, expectedPath)
	if err != nil {
		// We could not identify the listener binary (e.g. lsof missing).
		// Health is already 200, so don't block the update on this; just
		// warn so the user knows verification was partial.
		fmt.Printf("warning: daemon is responding on :%d but we could not verify its binary path (%v)\n", port, err)
		return nil
	}
	if conflict != "" {
		return fmt.Errorf("daemon is up but owned by a different binary: %s", conflict)
	}
	return nil
}

// rollbackAfterInstall restores the backed-up binary after a failed
// post-install step. If a service was running before the update, also
// re-register the launchd plist against the restored binary so the old
// version keeps serving. All steps are best-effort: if any individual
// operation fails, we still try the remaining ones because the user is
// already in a broken state and we want to leave them as close to the
// pre-update world as possible.
func rollbackAfterInstall(selfPath, backup string, serviceWasRunning bool) {
	_ = os.Remove(selfPath)
	_ = os.Rename(backup, selfPath)
	if runtime.GOOS != "darwin" || !serviceWasRunning {
		return
	}
	_ = service.Uninstall()
	_ = service.Install(selfPath, updateDaemonPort)
}

func parseUpdateFlags(args []string) updateOptions {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	opts := updateOptions{}
	fs.BoolVar(&opts.check, "check", false, "Only report whether an update is available; do not download or install.")
	fs.BoolVar(&opts.force, "force", false, "Re-install even if already at target version and ignore daemon conflict warnings.")
	fs.StringVar(&opts.toVer, "to", "", "Install this specific release (e.g. v0.6.0) instead of the latest one.")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: cc-clip update [--check] [--force] [--to VERSION]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "cc-clip update takes no positional arguments.")
		fs.Usage()
		os.Exit(2)
	}
	return opts
}

func resolveSelfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	abs, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return abs, nil
}

// normalizeVersion canonicalizes a version string into "vX.Y.Z" form, or
// returns "" for anything that is not a clean release tag.
//
// Things we deliberately return "" for:
//   - "dev" and empty: obvious non-release builds.
//   - Bare commit SHAs (no dots).
//   - `git describe` output between tags: `v0.6.1-1-g4d2038b`. These indicate
//     a dev binary built off an untagged commit; treating them as "already at
//     v0.6.1" would mask actual upgrades.
//   - Dirty-tree markers (`-dirty` suffix). Same reason.
//
// Things we keep (so real release tags normalize cleanly):
//   - Plain versions like `v0.6.1`, `0.6.1`.
//   - Prerelease versions like `v0.6.1-rc1`, `v0.6.1-beta.2`.
//   - Build metadata like `v0.6.1+build.7`.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return ""
	}
	v = strings.TrimPrefix(v, "cc-clip ")
	v = strings.TrimPrefix(v, "v")
	if !looksLikeSemver(v) {
		return ""
	}
	return "v" + v
}

// gitDescribeMarker matches the "-<commits>-g<sha>" suffix that
// `git describe --tags --always --dirty` appends between release tags.
// The sha portion is at least 4 hex chars in practice.
var gitDescribeMarker = regexp.MustCompile(`-\d+-g[0-9a-f]{4,}`)

func looksLikeSemver(v string) bool {
	if gitDescribeMarker.MatchString(v) {
		return false
	}
	if strings.HasSuffix(v, "-dirty") {
		return false
	}
	parts := strings.SplitN(v, ".", 4)
	if len(parts) < 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		// allow trailing suffixes on the patch component (e.g. 0-rc1)
		s := parts[i]
		if i == 2 {
			if idx := strings.IndexAny(s, "-+"); idx >= 0 {
				s = s[:idx]
			}
		}
		if _, err := strconv.Atoi(s); err != nil {
			return false
		}
	}
	return true
}

func displayVersion(v string) string {
	if v == "" {
		return "(unknown; probably a dev build)"
	}
	return v
}

func fetchLatestReleaseTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", updateAPIURL+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cc-clip-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var data struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("parse release JSON: %w", err)
	}
	if data.Draft || data.Prerelease {
		return "", fmt.Errorf("latest release is marked draft/prerelease: %s", data.TagName)
	}
	if data.TagName == "" {
		return "", errors.New("GitHub API returned empty tag_name")
	}
	return data.TagName, nil
}

// detectDaemonConflict returns a human-readable conflict description if port
// is currently held by a listener whose binary is NOT selfPath. Returns an
// empty string when there is no conflict, or an error if detection itself
// could not run (e.g. lsof missing).
func detectDaemonConflict(port int, selfPath string) (string, error) {
	if _, err := exec.LookPath("lsof"); err != nil {
		return "", fmt.Errorf("lsof not available; skipping conflict check: %w", err)
	}
	cmd := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-Fpn")
	out, err := cmd.Output()
	if err != nil {
		// lsof returns exit code 1 when there's no match; that's fine.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("lsof failed: %w", err)
	}
	pid, err := parseLsofPID(string(out))
	if err != nil {
		return "", err
	}
	if pid == 0 {
		return "", nil
	}
	binary, err := readProcessBinary(pid)
	if err != nil {
		return "", err
	}
	if binary == "" {
		return "", nil
	}
	if samePath(binary, selfPath) {
		return "", nil
	}
	return fmt.Sprintf("port %d is held by PID %d running %s (not %s)", port, pid, binary, selfPath), nil
}

// parseLsofPID extracts the listener PID from the output of
// `lsof -nP -iTCP:<port> -sTCP:LISTEN -Fpn`. Returns 0 when no listener.
// Format: records start with "p<pid>" lines followed by "n<ip>:<port>".
// We take the first pid seen.
func parseLsofPID(raw string) (int, error) {
	for _, line := range strings.Split(raw, "\n") {
		if len(line) < 2 || line[0] != 'p' {
			continue
		}
		pid, err := strconv.Atoi(line[1:])
		if err != nil {
			return 0, fmt.Errorf("lsof p-line malformed: %q", line)
		}
		return pid, nil
	}
	return 0, nil
}

// readProcessBinary returns the absolute path to the binary executing the
// given pid, or "" if we cannot resolve one on this platform.
//
// We deliberately do NOT use `ps -o comm=`: on Linux that returns just the
// basename (often truncated to 15 chars), and on macOS it also truncates.
// Comparing a truncated basename against an absolute path would flag every
// listener as a conflict even when it IS the binary we are about to replace.
// When the OS cannot give us an absolute path, we return "" so the caller
// treats it as "conflict unknown, skip" rather than a hard abort.
func readProcessBinary(pid int) (string, error) {
	switch runtime.GOOS {
	case "linux":
		// /proc/<pid>/exe is a symlink to the absolute executable path.
		target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			return "", fmt.Errorf("read /proc/%d/exe: %w", pid, err)
		}
		if !filepath.IsAbs(target) {
			return "", nil
		}
		return target, nil
	case "darwin":
		// `lsof -p <pid> -a -d txt -Fn` lists the text-segment files
		// (executable + libraries). The first absolute path is the
		// executable itself.
		cmd := exec.Command("lsof", "-p", strconv.Itoa(pid), "-a", "-d", "txt", "-Fn")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("lsof -p %d: %w", pid, err)
		}
		return parseLsofTextPath(string(out)), nil
	}
	return "", nil
}

// parseLsofTextPath scans output from `lsof -Fn -d txt` and returns the
// first absolute path. An "n" record in -F output looks like "n/abs/path".
func parseLsofTextPath(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if len(line) > 1 && line[0] == 'n' && line[1] == '/' {
			return line[1:]
		}
	}
	return ""
}

func samePath(a, b string) bool {
	aa, err := filepath.EvalSymlinks(a)
	if err == nil {
		a = aa
	}
	bb, err := filepath.EvalSymlinks(b)
	if err == nil {
		b = bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// releaseArchiveName returns the filename goreleaser publishes for a given
// release tag + target triple. This is the single place where the
// cc-clip-update path encodes its assumption about release asset naming.
//
// The same filename shape appears in two other places that MUST stay aligned:
//
//   - `.goreleaser.yaml` `name_template` (what goreleaser actually emits).
//   - `scripts/install.sh` `ARCHIVE_NAME` (what the install-script path
//     expects).
//
// `make release-preflight` greps for this constant in update.go alongside
// the goreleaser/install.sh checks, so any future drift in one spot fails
// the contract gate before a tag can be pushed.
func releaseArchiveName(tag, goos, goarch string) string {
	version := strings.TrimPrefix(tag, "v")
	return fmt.Sprintf("cc-clip_%s_%s_%s.tar.gz", version, goos, goarch)
}

func downloadAndVerifyArchive(ctx context.Context, tag string) (string, func(), error) {
	archiveName := releaseArchiveName(tag, runtime.GOOS, runtime.GOARCH)

	tmpDir, err := os.MkdirTemp("", "cc-clip-update-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	archivePath := filepath.Join(tmpDir, archiveName)
	checksumPath := filepath.Join(tmpDir, "checksums.txt")

	archiveURL := fmt.Sprintf("%s/%s/%s", updateDownloadBase, tag, archiveName)
	checksumURL := fmt.Sprintf("%s/%s/checksums.txt", updateDownloadBase, tag)

	fmt.Printf("downloading %s\n", archiveName)
	if err := httpDownload(ctx, archiveURL, archivePath); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("download %s: %w", archiveName, err)
	}
	fmt.Println("downloading checksums.txt")
	if err := httpDownload(ctx, checksumURL, checksumPath); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("download checksums.txt: %w", err)
	}

	if err := verifySHA256(archivePath, checksumPath, archiveName); err != nil {
		cleanup()
		return "", func() {}, err
	}
	fmt.Println("checksum verified")

	return archivePath, cleanup, nil
}

func httpDownload(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cc-clip-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func verifySHA256(archivePath, checksumsPath, archiveName string) error {
	want, err := lookupChecksum(checksumsPath, archiveName)
	if err != nil {
		return err
	}
	got, err := fileSHA256(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s", archiveName, got, want)
	}
	return nil
}

func lookupChecksum(checksumsPath, archiveName string) (string, error) {
	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == archiveName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s in checksums.txt", archiveName)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractBinary(archivePath string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "cc-clip-update-extract-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	f, err := os.Open(archivePath)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != "cc-clip" {
			continue
		}
		dest := filepath.Join(tmpDir, "cc-clip")
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			cleanup()
			return "", func() {}, err
		}
		out.Close()
		return dest, cleanup, nil
	}
	cleanup()
	return "", func() {}, errors.New("cc-clip binary not found inside archive")
}

// prepareMacOSBinary clears quarantine xattrs and re-signs with an ad-hoc
// identity so Gatekeeper allows the downloaded binary to execute. Mirrors
// what scripts/install.sh does at the end.
func prepareMacOSBinary(path string) error {
	// xattr -cr: clear all xattrs recursively (including com.apple.quarantine
	// and com.apple.provenance). Best-effort; failure here is not fatal.
	_ = exec.Command("xattr", "-cr", path).Run()
	cmd := exec.Command("codesign", "--force", "--sign", "-", "--identifier", "com.cc-clip.cli", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("codesign failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func renameAtomic(src, dst string) error {
	// os.Rename is atomic on POSIX within the same filesystem. The extract
	// tmp dir may be on /tmp which is often on a different device; copy in
	// that case.
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".new", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst + ".new")
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst + ".new")
		return err
	}
	return os.Rename(dst+".new", dst)
}

// runVersionCommand executes `<binaryPath> --version` with a hard timeout
// so a hanging binary (bad linked library, waiting on some external
// resource at startup, etc.) cannot wedge the updater. This matters most
// AFTER the swap: a post-install verify that blocks forever means no
// rollback runs, leaving the user with a broken binary and no recourse.
func runVersionCommand(binaryPath string) (string, error) {
	return runVersionCommandWithTimeout(binaryPath, 10*time.Second)
}

func runVersionCommandWithTimeout(binaryPath string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	// Without WaitDelay, killing the direct child still leaves orphaned
	// descendants holding the stdout pipe open; cmd.Output() would then
	// wait indefinitely for EOF. WaitDelay forces the pipes closed after
	// the grace window, so Output returns promptly even if a grandchild
	// (shell -> sleep) is still writing.
	cmd.WaitDelay = 1 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func printPostUpdateReminders(targetTag, binaryPath string, serviceRestarted bool) {
	fmt.Println()
	fmt.Println("== Post-update checklist ==")
	if !serviceRestarted {
		fmt.Println("* Restart the cc-clip daemon so it runs the new binary.")
		fmt.Println("  (macOS) cc-clip service uninstall && cc-clip service install")
		fmt.Println("  (Linux foreground) stop your running `cc-clip serve` and start it again.")
	}
	fmt.Println("* Redeploy to every remote host you use with cc-clip:")
	fmt.Println("    cc-clip connect <host> --force")
	fmt.Println("    cc-clip connect <host> --codex --force   # if you use Codex CLI there")
	fmt.Println()
	fmt.Printf("cc-clip %s installed at %s.\n", strings.TrimPrefix(targetTag, "v"), binaryPath)
}
