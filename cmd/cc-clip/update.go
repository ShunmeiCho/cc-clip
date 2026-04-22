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

	backup := selfPath + ".bak"
	if err := os.Rename(selfPath, backup); err != nil {
		log.Fatalf("failed to back up current binary: %v", err)
	}
	rollback := func() {
		_ = os.Rename(backup, selfPath)
	}

	if err := renameAtomic(stagedBinary, selfPath); err != nil {
		rollback()
		log.Fatalf("failed to install new binary (rolled back): %v", err)
	}
	if err := os.Chmod(selfPath, 0o755); err != nil {
		rollback()
		log.Fatalf("failed to chmod new binary (rolled back): %v", err)
	}

	fmt.Printf("binary installed: %s\n", selfPath)

	serviceWasRunning := restartDaemon(selfPath)

	installedTag, verifyErr := runVersionCommand(selfPath)
	if verifyErr != nil {
		fmt.Printf("warning: could not verify installed version: %v\n", verifyErr)
	} else if installedTag != target && !strings.HasSuffix(installedTag, strings.TrimPrefix(target, "v")) {
		fmt.Printf("warning: installed binary reports %q, expected %s\n", installedTag, target)
	} else {
		fmt.Printf("verified: %s\n", installedTag)
	}

	_ = os.Remove(backup)

	printPostUpdateReminders(target, selfPath, serviceWasRunning)
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
// returns "" for unknown versions like "dev" or a git-describe SHA.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return ""
	}
	v = strings.TrimPrefix(v, "cc-clip ")
	v = strings.TrimPrefix(v, "v")
	// Reject anything that doesn't look like semver (rejects "abc123" SHAs).
	if !looksLikeSemver(v) {
		return ""
	}
	return "v" + v
}

func looksLikeSemver(v string) bool {
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

func readProcessBinary(pid int) (string, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ps failed for pid %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)), nil
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

func downloadAndVerifyArchive(ctx context.Context, tag string) (string, func(), error) {
	version := strings.TrimPrefix(tag, "v")
	archiveName := fmt.Sprintf("cc-clip_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)

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

// restartDaemon re-registers the launchd service (on macOS) so it picks up
// the new binary. Returns true if the service was running (and got
// reinstalled), false if there was no service to manage (e.g., the user runs
// the daemon in the foreground or uses systemd we can't speak to yet).
func restartDaemon(binaryPath string) bool {
	if runtime.GOOS != "darwin" {
		// On Linux we don't manage systemd yet; just note it.
		fmt.Println("note: on Linux, restart the daemon yourself if you run it as a service.")
		return false
	}
	running, err := service.Status()
	if err != nil || !running {
		fmt.Println("note: cc-clip service not detected as running; skipping service restart.")
		return false
	}
	fmt.Println("restarting launchd service...")
	if err := service.Uninstall(); err != nil {
		fmt.Printf("warning: service uninstall failed: %v\n", err)
	}
	if err := service.Install(binaryPath, updateDaemonPort); err != nil {
		fmt.Printf("warning: service install failed: %v\nyou may need to run `cc-clip service install` manually.\n", err)
		return false
	}
	return true
}

func runVersionCommand(binaryPath string) (string, error) {
	cmd := exec.Command(binaryPath, "--version")
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
