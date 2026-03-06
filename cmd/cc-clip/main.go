package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/doctor"
	"github.com/shunmei/cc-clip/internal/exitcode"
	"github.com/shunmei/cc-clip/internal/service"
	"github.com/shunmei/cc-clip/internal/setup"
	"github.com/shunmei/cc-clip/internal/shim"
	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
)

var version = "dev"

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe()
	case "paste":
		cmdPaste()
	case "install":
		cmdInstall()
	case "uninstall":
		cmdUninstall()
	case "connect":
		cmdConnect()
	case "status":
		cmdStatus()
	case "doctor":
		cmdDoctor()
	case "setup":
		cmdSetup()
	case "service":
		cmdService()
	case "version":
		fmt.Printf("cc-clip %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`cc-clip - Clipboard over SSH for Claude Code

Usage:
  cc-clip <command> [flags]

Daemon (local):
  serve              Start local clipboard daemon
    --port           Listen port (default: 18339, env: CC_CLIP_PORT)
    --rotate-token   Force new token generation (ignore existing)
  service            Manage launchd service (macOS)
    install          Install and load launchd service
    uninstall        Unload and remove launchd service
    status           Show launchd service status

Remote:
  install            Install xclip/wl-paste shim
    --target         auto|xclip|wl-paste (default: auto)
    --path           Install directory (default: ~/.local/bin)
  uninstall          Remove shim
    --host           Also clean up PATH marker on remote host
  paste              Fetch clipboard image and output path
    --out-dir        Output directory (env: CC_CLIP_OUT_DIR)

One-command setup:
  setup <host>       Full setup: deps, SSH config, daemon, deploy
    --port           Tunnel port (default: 18339)

Deploy (local -> remote):
  connect <host>     Deploy cc-clip to remote and establish session
    --port           Tunnel port (default: 18339)
    --local-bin      Path to pre-downloaded remote binary
    --force          Ignore remote state, full redeploy
    --token-only     Only sync token, skip binary/shim deploy

Diagnostics:
  status             Show component status
  doctor             Local health check
  doctor --host H    Full end-to-end check via SSH
  version            Show version`)
}

func getPort() int {
	port := 18339
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			if p, err := strconv.Atoi(os.Args[i+1]); err == nil {
				port = p
			}
		}
	}
	if env := os.Getenv("CC_CLIP_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			port = p
		}
	}
	return port
}

func getFlag(name, fallback string) string {
	for i, arg := range os.Args {
		if arg == "--"+name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return fallback
}

func hasFlag(name string) bool {
	for _, arg := range os.Args {
		if arg == "--"+name {
			return true
		}
	}
	return false
}

func getTokenTTL() time.Duration {
	ttl := 12 * time.Hour
	if env := os.Getenv("CC_CLIP_TOKEN_TTL"); env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			ttl = d
		}
	}
	return ttl
}

func cmdServe() {
	port := getPort()
	ttl := getTokenTTL()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	rotateToken := hasFlag("rotate-token")

	tm := token.NewManager(ttl)

	var session token.Session
	var reused bool
	var err error

	if rotateToken {
		session, err = tm.Generate()
		if err != nil {
			log.Fatalf("failed to generate token: %v", err)
		}
		log.Printf("Token rotated (--rotate-token): new token generated")
	} else {
		session, reused, err = tm.LoadOrGenerate(ttl)
		if err != nil {
			log.Fatalf("failed to load or generate token: %v", err)
		}
		if reused {
			log.Printf("Token reused from existing file (expires %s)", session.ExpiresAt.Format(time.RFC3339))
		} else {
			log.Printf("Token generated (no valid existing token found)")
		}
	}

	tokenPath, err := token.WriteTokenFile(session.Token, session.ExpiresAt)
	if err != nil {
		log.Fatalf("failed to write token file: %v", err)
	}

	clipboard := daemon.NewClipboardReader()
	srv := daemon.NewServer(addr, clipboard, tm)

	log.Printf("Token written to: %s", tokenPath)
	log.Printf("Token expires at: %s", session.ExpiresAt.Format(time.RFC3339))
	log.Printf("Starting daemon on %s", addr)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func cmdPaste() {
	port := getPort()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	tok, err := token.ReadTokenFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip: cannot read token: %v\n", err)
		os.Exit(exitcode.TokenInvalid)
	}

	probeTimeout := envDuration("CC_CLIP_PROBE_TIMEOUT_MS", 500*time.Millisecond)
	fetchTimeout := envDuration("CC_CLIP_FETCH_TIMEOUT_MS", 5*time.Second)

	if err := tunnel.Probe(fmt.Sprintf("127.0.0.1:%d", port), probeTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip: tunnel unreachable: %v\n", err)
		os.Exit(exitcode.TunnelUnreachable)
	}

	client := tunnel.NewClient(baseURL, tok, fetchTimeout)

	info, err := client.ClipboardType()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip: %v\n", err)
		os.Exit(classifyError(err))
	}

	if info.Type != daemon.ClipboardImage {
		fmt.Fprintf(os.Stderr, "cc-clip: no image in clipboard (type: %s)\n", info.Type)
		os.Exit(exitcode.NoImage)
	}

	outDir := tunnel.DefaultOutDir()
	if env := os.Getenv("CC_CLIP_OUT_DIR"); env != "" {
		outDir = env
	}
	if flag := getFlag("out-dir", ""); flag != "" {
		outDir = flag
	}

	path, err := client.FetchImage(outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip: %v\n", err)
		os.Exit(classifyError(err))
	}

	fmt.Println(path)
}

func cmdInstall() {
	targetStr := getFlag("target", "auto")
	installPath := getFlag("path", "")
	port := getPort()

	var target shim.Target
	switch targetStr {
	case "auto":
		target = shim.TargetAuto
	case "xclip":
		target = shim.TargetXclip
	case "wl-paste":
		target = shim.TargetWlPaste
	default:
		log.Fatalf("unsupported target: %s", targetStr)
	}

	result, err := shim.Install(target, installPath, port)
	if err != nil {
		log.Fatalf("install failed: %v", err)
	}

	fmt.Printf("Shim installed:\n")
	fmt.Printf("  target:    %s\n", result.Target)
	fmt.Printf("  shim:      %s\n", result.ShimPath)
	fmt.Printf("  real bin:  %s\n", result.RealBinPath)

	ok, msg := shim.CheckPathPriority(result.InstallDir)
	if ok {
		fmt.Printf("  PATH:      %s\n", msg)
	} else {
		fmt.Printf("  WARNING:   %s\n", msg)
		fmt.Printf("  Fix: add to ~/.bashrc or ~/.profile:\n")
		fmt.Printf("    export PATH=\"%s:$PATH\"\n", result.InstallDir)
	}
}

func cmdUninstall() {
	targetStr := getFlag("target", "auto")
	installPath := getFlag("path", "")
	host := getFlag("host", "")

	var target shim.Target
	switch targetStr {
	case "auto":
		target = shim.TargetAuto
	case "xclip":
		target = shim.TargetXclip
	case "wl-paste":
		target = shim.TargetWlPaste
	default:
		log.Fatalf("unsupported target: %s", targetStr)
	}

	if err := shim.Uninstall(target, installPath); err != nil {
		log.Fatalf("uninstall failed: %v", err)
	}

	fmt.Println("Shim removed successfully.")

	if host != "" {
		fmt.Printf("Removing PATH marker from remote %s...\n", host)
		if err := shim.RemoveRemotePath(host); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove PATH marker: %v\n", err)
		} else {
			fmt.Println("PATH marker removed from remote shell rc file.")
		}
	}
}

type connectOpts struct {
	host      string
	port      int
	force     bool
	tokenOnly bool
}

func cmdConnect() {
	if len(os.Args) < 3 {
		log.Fatal("usage: cc-clip connect <host> [--port PORT] [--force] [--token-only]")
	}
	runConnect(connectOpts{
		host:      os.Args[2],
		port:      getPort(),
		force:     hasFlag("force"),
		tokenOnly: hasFlag("token-only"),
	})
}

func runConnect(opts connectOpts) {
	host := opts.host
	port := opts.port
	force := opts.force
	tokenOnly := opts.tokenOnly

	// Step 1: Check local daemon
	fmt.Printf("[1/7] Checking local daemon on :%d...\n", port)
	probeTimeout := envDuration("CC_CLIP_PROBE_TIMEOUT_MS", 500*time.Millisecond)
	if err := tunnel.Probe(fmt.Sprintf("127.0.0.1:%d", port), probeTimeout); err != nil {
		log.Fatalf("Local daemon not running. Start it first: cc-clip serve")
	}
	fmt.Println("      daemon running")

	// Read the token that `cc-clip serve` already generated and holds in memory.
	// This is the token the daemon validates against — we must send this exact token to the remote.
	daemonToken, err := token.ReadTokenFile()
	if err != nil {
		log.Fatalf("      cannot read daemon token (is 'cc-clip serve' running?): %v", err)
	}

	// Step 2: Start SSH master session (passphrase prompted once here)
	fmt.Printf("[2/7] Establishing SSH session to %s...\n", host)
	session, err := shim.NewSSHSession(host)
	if err != nil {
		log.Fatalf("      failed: %v", err)
	}
	defer session.Close()
	fmt.Println("      SSH master connected")

	// --token-only: skip binary/shim, just sync token and verify tunnel
	if tokenOnly {
		fmt.Println("[3/7] Skipping binary check (--token-only)")
		fmt.Println("[4/7] Skipping binary upload (--token-only)")
		fmt.Println("[5/7] Skipping shim install (--token-only)")

		fmt.Printf("[6/7] Syncing token...\n")
		if err := shim.WriteRemoteTokenViaSession(session, daemonToken); err != nil {
			log.Fatalf("      failed to write token: %v", err)
		}
		fmt.Println("      token synced from local daemon")

		connectVerifyTunnel(session, port, host)
		return
	}

	// Step 3: Read remote deploy state and detect arch
	fmt.Printf("[3/7] Checking remote state...\n")
	remoteState, err := shim.ReadRemoteState(session)
	if err != nil {
		log.Printf("      warning: could not read remote state: %v", err)
	}
	if remoteState != nil && !force {
		fmt.Printf("      remote state: binary=%s shim=%v\n", remoteState.BinaryVersion, remoteState.ShimInstalled)
	} else if force {
		fmt.Println("      --force: ignoring remote state")
		remoteState = nil
	} else {
		fmt.Println("      no previous deploy state")
	}

	remoteOS, remoteArch, err := shim.DetectRemoteArchViaSession(session)
	if err != nil {
		log.Fatalf("      failed to detect remote arch: %v", err)
	}
	fmt.Printf("      %s/%s\n", remoteOS, remoteArch)

	remoteBin := "~/.local/bin/cc-clip"

	// Step 4: Prepare and upload binary (skip if hash matches)
	localBin, err := prepareBinaryLocal(host, remoteOS, remoteArch)
	if err != nil {
		log.Fatalf("[4/7] Prepare binary failed: %v", err)
	}

	needsUpload := force || shim.NeedsUpload(localBin, remoteState)
	if !needsUpload {
		// Verify the remote binary actually exists — deploy state can be stale.
		if _, err := session.Exec(fmt.Sprintf("test -x %s", remoteBin)); err != nil {
			fmt.Println("[4/7] Remote binary missing despite cached state, re-uploading")
			needsUpload = true
		}
	}
	if needsUpload {
		fmt.Printf("[4/7] Uploading cc-clip binary...\n")
		// Ensure remote directory exists
		session.Exec("mkdir -p ~/.local/bin")
		if err := shim.UploadBinaryViaSession(session, localBin, remoteBin); err != nil {
			log.Fatalf("      failed: %v", err)
		}
		fmt.Printf("      uploaded to %s\n", remoteBin)
	} else {
		fmt.Println("[4/7] Binary up to date, skipping upload")
	}

	// Step 5: Install shim (skip if already installed and not forced)
	needsShim := force || shim.NeedsShimInstall(remoteState)
	if !needsShim {
		// Verify the shim file actually exists — cached state can be stale.
		shimTarget := "xclip"
		if remoteState != nil && remoteState.ShimTarget != "" {
			shimTarget = remoteState.ShimTarget
		}
		checkCmd := fmt.Sprintf("test -f ~/.local/bin/%s && head -1 ~/.local/bin/%s | grep -q cc-clip", shimTarget, shimTarget)
		if _, err := session.Exec(checkCmd); err != nil {
			fmt.Println("      shim missing despite cached state, will reinstall")
			needsShim = true
		}
	}
	if needsShim {
		fmt.Printf("[5/7] Installing shim...\n")
		installCmd := fmt.Sprintf("%s install --port %d", remoteBin, port)
		out, err := session.Exec(installCmd)
		if err != nil {
			// Shim might already exist, try uninstall then install
			session.Exec(fmt.Sprintf("%s uninstall", remoteBin))
			out, err = session.Exec(installCmd)
			if err != nil {
				log.Fatalf("      remote install failed: %s: %v", out, err)
			}
		}
		fmt.Printf("      %s\n", out)
	} else {
		fmt.Println("[5/7] Shim already installed, skipping")
	}

	// Step 5b: Fix PATH if needed — always re-check, don't trust cached state
	var pathFixed bool
	fixed, pathErr := shim.IsPathFixedSession(session)
	if pathErr != nil {
		log.Printf("      warning: could not check PATH: %v", pathErr)
	} else if !fixed {
		fmt.Printf("      fixing remote PATH...\n")
		if err := shim.FixRemotePathSession(session); err != nil {
			log.Printf("      warning: PATH fix failed: %v", err)
		} else {
			pathFixed = true
			fmt.Println("      PATH marker injected")
		}
	} else {
		pathFixed = true
	}

	// Step 6: Always sync token
	fmt.Printf("[6/7] Syncing token...\n")
	if err := shim.WriteRemoteTokenViaSession(session, daemonToken); err != nil {
		log.Fatalf("      failed to write token: %v", err)
	}
	fmt.Println("      token synced from local daemon")

	// Update remote deploy state
	localHash, _ := shim.LocalBinaryHash(localBin)
	newState := &shim.DeployState{
		BinaryHash:    localHash,
		BinaryVersion: version,
		ShimInstalled: true,
		ShimTarget:    "xclip",
		PathFixed:     pathFixed,
	}
	if remoteState != nil && remoteState.ShimTarget != "" {
		newState.ShimTarget = remoteState.ShimTarget
	}
	if err := shim.WriteRemoteState(session, newState); err != nil {
		log.Printf("      warning: could not write remote deploy state: %v", err)
	}

	// Step 7: Verify tunnel
	connectVerifyTunnel(session, port, host)
}

func cmdSetup() {
	if len(os.Args) < 3 {
		log.Fatal("usage: cc-clip setup <host> [--port PORT]")
	}
	host := os.Args[2]
	port := getPort()

	// Step 1: Dependencies
	fmt.Println("[1/4] Checking local dependencies...")
	if runtime.GOOS == "darwin" {
		if p := setup.CheckPngpaste(); p != "" {
			fmt.Printf("      pngpaste: %s\n", p)
		} else {
			fmt.Println("      pngpaste not found, installing via Homebrew...")
			if err := setup.InstallPngpaste(); err != nil {
				log.Fatalf("      %v", err)
			}
			if p := setup.CheckPngpaste(); p != "" {
				fmt.Printf("      pngpaste: installed (%s)\n", p)
			}
		}
	} else {
		fmt.Println("      skipped (not macOS)")
	}

	// Step 2: SSH config
	fmt.Printf("[2/4] Configuring SSH for %s...\n", host)
	changes, err := setup.EnsureSSHConfig(host, port)
	if err != nil {
		log.Fatalf("      %v", err)
	}
	for _, c := range changes {
		fmt.Printf("      %s: %s\n", c.Action, c.Detail)
	}

	// Step 3: Daemon
	fmt.Println("[3/4] Starting local daemon...")
	probeTimeout := envDuration("CC_CLIP_PROBE_TIMEOUT_MS", 500*time.Millisecond)
	if err := tunnel.Probe(fmt.Sprintf("127.0.0.1:%d", port), probeTimeout); err == nil {
		fmt.Printf("      daemon already running on :%d\n", port)
	} else if runtime.GOOS == "darwin" {
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("      cannot determine executable path: %v", err)
		}
		exePath, _ = filepath.EvalSymlinks(exePath)
		if err := service.Install(exePath, port); err != nil {
			log.Fatalf("      service install failed: %v", err)
		}
		fmt.Println("      launchd service installed and started")
		// Wait for daemon to be ready
		time.Sleep(500 * time.Millisecond)
	} else {
		log.Fatal("      daemon not running. Start it first: cc-clip serve")
	}

	// Step 4: Deploy to remote
	fmt.Printf("\n[4/4] Deploying to %s...\n", host)
	runConnect(connectOpts{
		host: host,
		port: port,
	})
}

// connectVerifyTunnel verifies the SSH tunnel from the remote side.
func connectVerifyTunnel(session *shim.SSHSession, port int, host string) {
	remoteBin := "~/.local/bin/cc-clip"

	fmt.Printf("[7/7] Verifying tunnel from remote...\n")
	probeCmd := fmt.Sprintf(
		"bash -c 'echo >/dev/tcp/127.0.0.1/%d' 2>/dev/null && echo 'tunnel:ok' || echo 'tunnel:fail'",
		port)
	probeOut, _ := session.Exec(probeCmd)

	if probeOut == "tunnel:ok" {
		fmt.Println("      tunnel verified (via existing SSH session)")
	} else {
		fmt.Println("      tunnel not detected (this is normal if no interactive SSH session is open)")
		fmt.Println("      The tunnel is provided by your SSH connection, not by 'cc-clip connect'.")
		fmt.Println("      Ensure your SSH session includes RemoteForward:")
		fmt.Printf("        ssh -R %d:127.0.0.1:%d %s\n", port, port, host)
		fmt.Println()
		fmt.Println("      Or add to ~/.ssh/config:")
		fmt.Printf("        Host %s\n", host)
		fmt.Printf("            RemoteForward %d 127.0.0.1:%d\n", port, port)
	}

	// Verify remote binary is functional
	shimTestCmd := fmt.Sprintf("%s status 2>&1", remoteBin)
	shimOut, shimErr := session.Exec(shimTestCmd)
	if shimErr != nil {
		fmt.Printf("      WARNING: remote cc-clip status failed: %s\n", shimOut)
		fmt.Println("      The remote binary may be missing or broken.")
		fmt.Println("      Re-run with --force to redeploy: cc-clip connect", host, "--force")
		os.Exit(1)
	}
	fmt.Printf("      %s\n", shimOut)

	fmt.Println()
	fmt.Println("Setup complete. Ctrl+V in remote Claude Code will paste images from your Mac.")
}

// prepareBinaryLocal resolves the local binary path without performing remote operations.
// Remote operations (mkdir, etc.) are done by the caller using the SSH session.
func prepareBinaryLocal(host, remoteOS, remoteArch string) (localBin string, err error) {
	// User-specified local binary takes highest priority
	if flagBin := getFlag("local-bin", ""); flagBin != "" {
		if _, err := os.Stat(flagBin); err != nil {
			return "", fmt.Errorf("specified --local-bin not found: %s", flagBin)
		}
		return flagBin, nil
	}

	if remoteOS == runtime.GOOS && remoteArch == runtime.GOARCH {
		// Same arch — use current binary
		localBin, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("cannot find current executable: %w", err)
		}
		return localBin, nil
	}

	// Different arch — try downloading matching release binary from GitHub
	fmt.Printf("      downloading cc-clip %s for %s/%s...\n", version, remoteOS, remoteArch)
	downloaded, dlErr := downloadReleaseBinary(remoteOS, remoteArch)
	if dlErr == nil {
		return downloaded, nil
	}
	fmt.Printf("      download failed: %v\n", dlErr)

	// Fallback: cross-compile (requires source + go toolchain)
	fmt.Printf("      trying cross-compile...\n")
	if _, lookErr := exec.LookPath("go"); lookErr != nil {
		return "", fmt.Errorf(
			"cannot obtain cc-clip for %s/%s:\n"+
				"  - GitHub release download failed: %v\n"+
				"  - Cross-compile unavailable: Go toolchain not found\n"+
				"  Fix: download the correct binary from https://github.com/ShunmeiCho/cc-clip/releases\n"+
				"       and re-run with: cc-clip connect %s --local-bin /path/to/cc-clip",
			remoteOS, remoteArch, dlErr, host)
	}

	srcDir, err := findSourceDir()
	if err != nil {
		return "", fmt.Errorf(
			"cannot obtain cc-clip for %s/%s:\n"+
				"  - GitHub release download failed: %v\n"+
				"  - Cross-compile unavailable: source directory not found\n"+
				"  Fix: download the correct binary from https://github.com/ShunmeiCho/cc-clip/releases\n"+
				"       and re-run with: cc-clip connect %s --local-bin /path/to/cc-clip",
			remoteOS, remoteArch, dlErr, host)
	}

	tmpBin := filepath.Join(os.TempDir(), fmt.Sprintf("cc-clip-%s-%s", remoteOS, remoteArch))
	buildCmd := exec.Command("sh", "-c",
		fmt.Sprintf("cd %s && GOOS=%s GOARCH=%s go build -o %s ./cmd/cc-clip/",
			srcDir, remoteOS, remoteArch, tmpBin))
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("cross-compile failed: %s: %w", string(out), err)
	}
	return tmpBin, nil
}

func downloadReleaseBinary(targetOS, targetArch string) (string, error) {
	if version == "dev" {
		return "", fmt.Errorf("running dev build, no release version to download")
	}

	// Normalize: goreleaser uses version without "v" prefix in asset names,
	// but tag names always have "v" prefix.
	ver := strings.TrimPrefix(version, "v")
	archiveName := fmt.Sprintf("cc-clip_%s_%s_%s.tar.gz", ver, targetOS, targetArch)
	url := fmt.Sprintf("https://github.com/ShunmeiCho/cc-clip/releases/download/v%s/%s", ver, archiveName)

	tmpDir, err := os.MkdirTemp("", "cc-clip-download-*")
	if err != nil {
		return "", err
	}

	archivePath := filepath.Join(tmpDir, archiveName)
	dlCmd := exec.Command("curl", "-fsSL", "--max-time", "30", "-o", archivePath, url)
	if out, err := dlCmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("download failed (%s): %s", url, string(out))
	}

	extractCmd := exec.Command("tar", "-xzf", archivePath, "-C", tmpDir)
	if out, err := extractCmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("extract failed: %s", string(out))
	}

	binPath := filepath.Join(tmpDir, "cc-clip")
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("binary not found in archive")
	}

	return binPath, nil
}

func findSourceDir() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
			dir = filepath.Dir(dir)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return cwd, nil
	}

	return "", fmt.Errorf("go.mod not found near executable or cwd")
}

func cmdDoctor() {
	port := getPort()
	host := getFlag("host", "")

	if host == "" {
		fmt.Println("cc-clip doctor (local)")
		fmt.Println()
		results := doctor.RunLocal(port)
		allOK := doctor.PrintResults(results)
		fmt.Println()
		if allOK {
			fmt.Println("All local checks passed.")
		} else {
			fmt.Println("Some checks failed. Fix the issues above.")
			os.Exit(1)
		}
	} else {
		fmt.Printf("cc-clip doctor (end-to-end: %s)\n", host)
		fmt.Println()

		fmt.Println("Local checks:")
		localResults := doctor.RunLocal(port)
		localOK := doctor.PrintResults(localResults)
		fmt.Println()

		fmt.Println("Remote checks:")
		remoteResults := doctor.RunRemote(host, port)
		remoteOK := doctor.PrintResults(remoteResults)
		fmt.Println()

		if localOK && remoteOK {
			fmt.Println("All checks passed. cc-clip is ready.")
		} else {
			fmt.Println("Some checks failed. Fix the issues above.")
			os.Exit(1)
		}
	}
}

func cmdStatus() {
	port := getPort()
	probeTimeout := envDuration("CC_CLIP_PROBE_TIMEOUT_MS", 500*time.Millisecond)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := tunnel.Probe(addr, probeTimeout); err != nil {
		fmt.Printf("daemon:  not running on :%d\n", port)
	} else {
		fmt.Printf("daemon:  running on :%d\n", port)
	}

	tok, err := token.ReadTokenFile()
	if err != nil {
		fmt.Println("token:   not found")
	} else {
		fmt.Printf("token:   present (%d chars)\n", len(tok))
	}

	tokenDir, dirErr := token.TokenDir()
	if dirErr == nil {
		tokenPath := filepath.Join(tokenDir, "session.token")
		if info, statErr := os.Stat(tokenPath); statErr == nil {
			age := time.Since(info.ModTime())
			fmt.Printf("token:   modified %s ago\n", formatStatusDuration(age))
		}
	}

	if runtime.GOOS == "darwin" {
		running, err := service.Status()
		if err == nil {
			if running {
				fmt.Println("launchd: running")
			} else {
				fmt.Println("launchd: not running")
			}
		} else {
			fmt.Println("launchd: not installed")
		}
	}

	fmt.Printf("out-dir: %s\n", tunnel.DefaultOutDir())
}

func formatStatusDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func cmdService() {
	if len(os.Args) < 3 {
		log.Fatal("usage: cc-clip service <install|uninstall|status>")
	}

	subcmd := os.Args[2]
	switch subcmd {
	case "install":
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("cannot determine executable path: %v", err)
		}
		exePath, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			log.Fatalf("cannot resolve executable path: %v", err)
		}
		port := getPort()
		if err := service.Install(exePath, port); err != nil {
			log.Fatalf("service install failed: %v", err)
		}
		fmt.Printf("Launchd service installed and loaded.\n")
		fmt.Printf("  plist: %s\n", service.PlistPath())
		fmt.Printf("  logs:  ~/Library/Logs/cc-clip.log\n")

	case "uninstall":
		if err := service.Uninstall(); err != nil {
			log.Fatalf("service uninstall failed: %v", err)
		}
		fmt.Println("Launchd service unloaded and removed.")

	case "status":
		running, err := service.Status()
		if err != nil {
			log.Fatalf("service status check failed: %v", err)
		}
		if running {
			fmt.Println("service: running (launchd)")
		} else {
			fmt.Println("service: not running")
		}

	default:
		log.Fatalf("unknown service subcommand: %s (use install, uninstall, or status)", subcmd)
	}
}

func classifyError(err error) int {
	if errors.Is(err, tunnel.ErrTokenInvalid) {
		return exitcode.TokenInvalid
	}
	if errors.Is(err, tunnel.ErrNoImage) {
		return exitcode.NoImage
	}
	return exitcode.DownloadFailed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	env := os.Getenv(key)
	if env == "" {
		return fallback
	}
	ms, err := strconv.Atoi(env)
	if err != nil {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}
