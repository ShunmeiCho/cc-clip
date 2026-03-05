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

Remote:
  install            Install xclip/wl-paste shim
    --target         auto|xclip|wl-paste (default: auto)
    --path           Install directory (default: ~/.local/bin)
  uninstall          Remove shim
  paste              Fetch clipboard image and output path
    --out-dir        Output directory (env: CC_CLIP_OUT_DIR)

Deploy (local -> remote):
  connect <host>     Deploy cc-clip to remote and establish session
    --port           Tunnel port (default: 18339)
    --local-bin      Path to pre-downloaded remote binary

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

	tm := token.NewManager(ttl)
	session, err := tm.Generate()
	if err != nil {
		log.Fatalf("failed to generate token: %v", err)
	}

	tokenPath, err := token.WriteTokenFile(session.Token)
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
}

func cmdConnect() {
	if len(os.Args) < 3 {
		log.Fatal("usage: cc-clip connect <host> [--port PORT]")
	}
	host := os.Args[2]
	port := getPort()

	// Step 1: Check local daemon
	fmt.Printf("[1/6] Checking local daemon on :%d...\n", port)
	probeTimeout := envDuration("CC_CLIP_PROBE_TIMEOUT_MS", 500*time.Millisecond)
	if err := tunnel.Probe(fmt.Sprintf("127.0.0.1:%d", port), probeTimeout); err != nil {
		log.Fatalf("Local daemon not running. Start it first: cc-clip serve")
	}
	fmt.Println("      daemon running")

	// Step 2: Detect remote arch
	fmt.Printf("[2/6] Detecting remote arch...\n")
	remoteOS, remoteArch, err := shim.DetectRemoteArch(host)
	if err != nil {
		log.Fatalf("      failed: %v", err)
	}
	fmt.Printf("      %s/%s\n", remoteOS, remoteArch)

	// Step 3: Build or locate binary for remote arch
	fmt.Printf("[3/6] Preparing binary for %s/%s...\n", remoteOS, remoteArch)
	localBin, remoteBin, err := prepareBinary(host, remoteOS, remoteArch)
	if err != nil {
		log.Fatalf("      failed: %v", err)
	}

	// Step 4: Upload binary
	fmt.Printf("[4/6] Uploading cc-clip binary...\n")
	if err := shim.UploadBinary(host, localBin, remoteBin); err != nil {
		log.Fatalf("      failed: %v", err)
	}
	fmt.Printf("      uploaded to %s\n", remoteBin)

	// Step 5: Install shim + write the SAME token that serve daemon uses
	fmt.Printf("[5/6] Installing shim and writing token...\n")

	// Read the token that `cc-clip serve` already generated and holds in memory.
	// This is the token the daemon validates against — we must send this exact token to the remote.
	daemonToken, err := token.ReadTokenFile()
	if err != nil {
		log.Fatalf("      cannot read daemon token (is 'cc-clip serve' running?): %v", err)
	}

	installCmd := fmt.Sprintf("%s install --port %d", remoteBin, port)
	out, err := shim.RemoteExec(host, installCmd)
	if err != nil {
		// Shim might already exist, try uninstall then install
		shim.RemoteExec(host, fmt.Sprintf("%s uninstall", remoteBin))
		out, err = shim.RemoteExec(host, installCmd)
		if err != nil {
			log.Fatalf("      remote install failed: %s: %v", out, err)
		}
	}
	fmt.Printf("      %s\n", out)

	if err := shim.WriteRemoteToken(host, daemonToken); err != nil {
		log.Fatalf("      failed to write token: %v", err)
	}
	fmt.Println("      token synced from local daemon")

	// Step 6: Verify tunnel connectivity from remote side
	fmt.Printf("[6/6] Verifying tunnel from remote...\n")
	probeCmd := fmt.Sprintf(
		"bash -c 'echo >/dev/tcp/127.0.0.1/%d' 2>/dev/null && echo 'tunnel:ok' || echo 'tunnel:fail'",
		port)
	probeOut, _ := shim.RemoteExec(host, probeCmd)

	if probeOut == "tunnel:ok" {
		fmt.Println("      tunnel verified")
	} else {
		fmt.Println("      WARNING: tunnel not reachable from remote")
		fmt.Println("      Ensure your SSH session includes RemoteForward:")
		fmt.Printf("        ssh -R %d:127.0.0.1:%d %s\n", port, port, host)
		fmt.Println()
		fmt.Println("      Or add to ~/.ssh/config:")
		fmt.Printf("        Host %s\n", host)
		fmt.Printf("            RemoteForward %d 127.0.0.1:%d\n", port, port)
		return
	}

	// Verify shim can reach daemon and get a response
	shimTestCmd := fmt.Sprintf("%s status 2>&1", remoteBin)
	shimOut, _ := shim.RemoteExec(host, shimTestCmd)
	fmt.Printf("      %s\n", shimOut)

	fmt.Println()
	fmt.Println("Setup complete. Ctrl+V in remote Claude Code will paste images from your Mac.")
}

func prepareBinary(host, remoteOS, remoteArch string) (localBin string, remoteBin string, err error) {
	remoteBin = "~/.local/bin/cc-clip"

	// Ensure remote directory exists
	shim.RemoteExec(host, "mkdir -p ~/.local/bin")

	// User-specified local binary takes highest priority
	if flagBin := getFlag("local-bin", ""); flagBin != "" {
		if _, err := os.Stat(flagBin); err != nil {
			return "", "", fmt.Errorf("specified --local-bin not found: %s", flagBin)
		}
		return flagBin, remoteBin, nil
	}

	if remoteOS == runtime.GOOS && remoteArch == runtime.GOARCH {
		// Same arch — use current binary
		localBin, err = os.Executable()
		if err != nil {
			return "", "", fmt.Errorf("cannot find current executable: %w", err)
		}
		return localBin, remoteBin, nil
	}

	// Different arch — try downloading matching release binary from GitHub
	fmt.Printf("      downloading cc-clip %s for %s/%s...\n", version, remoteOS, remoteArch)
	downloaded, dlErr := downloadReleaseBinary(remoteOS, remoteArch)
	if dlErr == nil {
		return downloaded, remoteBin, nil
	}
	fmt.Printf("      download failed: %v\n", dlErr)

	// Fallback: cross-compile (requires source + go toolchain)
	fmt.Printf("      trying cross-compile...\n")
	if _, lookErr := exec.LookPath("go"); lookErr != nil {
		return "", "", fmt.Errorf(
			"cannot obtain cc-clip for %s/%s:\n"+
				"  - GitHub release download failed: %v\n"+
				"  - Cross-compile unavailable: Go toolchain not found\n"+
				"  Fix: download the correct binary from https://github.com/shunmei/cc-clip/releases\n"+
				"       and re-run with: cc-clip connect %s --local-bin /path/to/cc-clip",
			remoteOS, remoteArch, dlErr, host)
	}

	srcDir, err := findSourceDir()
	if err != nil {
		return "", "", fmt.Errorf(
			"cannot obtain cc-clip for %s/%s:\n"+
				"  - GitHub release download failed: %v\n"+
				"  - Cross-compile unavailable: source directory not found\n"+
				"  Fix: download the correct binary from https://github.com/shunmei/cc-clip/releases\n"+
				"       and re-run with: cc-clip connect %s --local-bin /path/to/cc-clip",
			remoteOS, remoteArch, dlErr, host)
	}

	tmpBin := filepath.Join(os.TempDir(), fmt.Sprintf("cc-clip-%s-%s", remoteOS, remoteArch))
	buildCmd := exec.Command("sh", "-c",
		fmt.Sprintf("cd %s && GOOS=%s GOARCH=%s go build -o %s ./cmd/cc-clip/",
			srcDir, remoteOS, remoteArch, tmpBin))
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("cross-compile failed: %s: %w", string(out), err)
	}
	return tmpBin, remoteBin, nil
}

func downloadReleaseBinary(targetOS, targetArch string) (string, error) {
	if version == "dev" {
		return "", fmt.Errorf("running dev build, no release version to download")
	}

	// Normalize: goreleaser uses version without "v" prefix in asset names,
	// but tag names always have "v" prefix.
	ver := strings.TrimPrefix(version, "v")
	archiveName := fmt.Sprintf("cc-clip_%s_%s_%s.tar.gz", ver, targetOS, targetArch)
	url := fmt.Sprintf("https://github.com/shunmei/cc-clip/releases/download/v%s/%s", ver, archiveName)

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

	fmt.Printf("out-dir: %s\n", tunnel.DefaultOutDir())
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
