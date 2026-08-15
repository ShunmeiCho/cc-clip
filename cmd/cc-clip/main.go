package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/doctor"
	"github.com/shunmei/cc-clip/internal/exitcode"
	"github.com/shunmei/cc-clip/internal/service"
	"github.com/shunmei/cc-clip/internal/shim"
	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
	"github.com/shunmei/cc-clip/internal/x11bridge"
	"github.com/shunmei/cc-clip/internal/xvfb"
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
	case "send":
		cmdSend()
	case "hotkey":
		cmdHotkey()
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
	case "hosts":
		cmdHosts()
	case "update":
		cmdUpdate()
	case "notify":
		cmdNotify()
	case "copy":
		cmdCopy()
	case "plugin":
		cmdPlugin()
	case "x11-bridge":
		cmdX11Bridge()
	case "version", "--version", "-v":
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
  service            Manage system service (macOS/Windows)
    install          Install and start service
    uninstall        Stop and remove service
    status           Show service status

Remote:
  install            Install xclip/wl-paste shim
    --target         auto|xclip|wl-paste (default: auto)
    --path           Install directory (default: ~/.local/bin)
  uninstall          Remove shim
    --host           Also clean up remote: Claude hooks/wrapper + PATH marker
  paste              Fetch clipboard image and output path
    --out-dir        Output directory (env: CC_CLIP_OUT_DIR)
  send [<host>] [<file>]
                      Upload local clipboard image or file to remote file path
    --file           Upload this image file instead of reading the clipboard
    --remote-dir     Remote directory (default: ~/.cache/cc-clip/uploads)
    --paste          On Windows, paste the remote path into the active window
    --delay-ms       Delay before Ctrl+Shift+V when --paste is used (default: 150)
    --no-restore     Do not restore the original image clipboard after --paste
  hotkey [<host>]    Windows global remote-paste hotkey listener
    --remote-dir     Remote directory (default: ~/.cache/cc-clip/uploads)
    --hotkey         Global hotkey to trigger remote paste (default: alt+shift+v)
    --delay-ms       Delay before Ctrl+Shift+V after the hotkey (default: 150)
    --enable-autostart   Start the hotkey automatically at login
    --disable-autostart  Remove hotkey auto-start at login
    --stop           Stop the background hotkey process
    --status         Show hotkey process status

One-command setup:
  setup <host>       Full setup: deps, SSH config, daemon, deploy
    --port           Tunnel port (default: 18339)
    --claude/--codex/--opencode/--agy/--cursor/--all   Deployment target (see "Deployment targets" below)
    --use-remote-bin Use cc-clip from the remote PATH; skip binary upload
    --auto-recover   Recover from v0.7.0 wrapper corruption (mutex with --token-only)

Known hosts (per-user registry):
  hosts list         Show hosts this machine has connected to (version, codex, last seen)
  hosts forget HOST  Stop tracking a host locally (remote is not touched)

Self-update (macOS/Linux):
  update             Download and install the latest cc-clip release
    --check          Only check whether a newer release exists; do not install
    --force          Re-install even if already at target version; ignore
                     conflict warnings from another daemon on the same port
    --to VERSION     Install a specific version (e.g. v0.6.0) instead of latest

Deploy (local -> remote):
  connect <host>     Deploy cc-clip to remote and establish session
    --port           Tunnel port (default: 18339)
    --local-bin      Path to pre-downloaded remote binary
    --use-remote-bin Use cc-clip from the remote PATH; skip binary upload
    --force          Ignore remote state, full redeploy
    --token-only     Only sync token, skip binary/shim deploy
    --no-hooks       Persistently disable Claude Code hook injection (Claude target only)
    --hooks          Re-enable Claude Code hook injection (Claude target only)
    --auto-recover   Recover from v0.7.0 wrapper corruption (mutex with --token-only)

Deployment targets (connect/setup; choose at most one selector):
    --claude         Claude Code: clipboard shim + claude-notify (default)
    --codex          Codex CLI ONLY: Xvfb + x11-bridge + codex-notify (no Claude shim)
    --opencode       opencode: clipboard shim + session.idle notify plugin
    --agy            Antigravity: agy-notify (alias --antigravity)
    --cursor         Cursor CLI: clipboard shim only (requires DISPLAY or
                     WAYLAND_DISPLAY in Cursor's shell; no notifications yet)
    --all            Everything above
  With no selector: interactive menu on a TTY, or the {Claude} default on a
  non-TTY. v0.9.0 BREAKING: --codex no longer installs the Claude shim; use
  --all for the previous Claude+Codex behavior.

Codex teardown:
  uninstall --codex        Remove Codex support only (local)
  uninstall --codex --host H  Remove Codex support on remote host

Diagnostics:
  status             Show component status
  doctor             Local health check
  doctor --host H    Full end-to-end check via SSH
  version            Show version

Copy (remote -> local):
  copy               Read stdin and place it on the LOCAL machine's clipboard
                     verbatim (run on the remote; needs the SSH tunnel). Piped
                     text carries none of the soft-wrap newlines that mouse
                     selection in a terminal injects:
                       cat file.txt | cc-clip copy
    --port           Tunnel port (default: 18339)

Notifications:
  notify             Send a notification to the local daemon
    --title              Notification title
    --body               Notification body
    --urgency            Urgency level (default: 1)
    --sound              macOS notification sound (allowlisted)
    --trusted            Suppress [unverified] prefix for trusted local config
    --from-codex         Parse Codex JSON payload (extracts last-assistant-message)
    --from-codex-stdin   Read Codex JSON payload from stdin (mutually exclusive with --from-codex)
    --port               Daemon port (default: 18339, env: CC_CLIP_PORT)

Internal (used by deploy):
  x11-bridge         X11 clipboard bridge daemon (started by connect --codex)
    --display        X11 display (default: $DISPLAY)
    --port           cc-clip daemon port (default: 18339)
  plugin run <name>  Run a notify adapter (claude-notify | codex-notify | agy-notify | opencode-notify)
                     reads agent hook JSON from stdin`)
}

func getPort() int {
	port := 18339
	if env := os.Getenv("CC_CLIP_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			port = p
		}
	}
	if flag := getFlag("port", ""); flag != "" {
		if p, err := strconv.Atoi(flag); err == nil {
			port = p
		}
	}
	return port
}

func getFlag(name, fallback string) string {
	if value, ok := flagValue(name); ok {
		return value
	}
	return fallback
}

func flagValue(name string) (string, bool) {
	prefix := "--" + name + "="
	for i, arg := range os.Args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix), true
		}
		if arg == "--"+name && i+1 < len(os.Args) {
			return os.Args[i+1], true
		}
	}
	return "", false
}

func hasFlag(name string) bool {
	prefix := "--" + name + "="
	for _, arg := range os.Args {
		if arg == "--"+name {
			return true
		}
		if strings.HasPrefix(arg, prefix) {
			value := strings.TrimPrefix(arg, prefix)
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return true
			}
			return enabled
		}
	}
	return false
}

func getTokenTTL() time.Duration {
	ttl := 30 * 24 * time.Hour
	if env := os.Getenv("CC_CLIP_TOKEN_TTL"); env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			ttl = d
		}
	}
	return ttl
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
	codex := hasFlag("codex")

	// --codex mode: only clean up Codex assets, don't touch Claude shim.
	if codex {
		if host != "" {
			cmdUninstallCodexRemote(host)
		} else {
			cmdUninstallCodexLocal()
		}
		return
	}

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
		if host == "" {
			log.Fatalf("uninstall failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "warning: local shim uninstall failed (continuing because --host was set): %v\n", err)
	} else {
		fmt.Println("Shim removed successfully.")
	}

	if host != "" {
		fmt.Printf("Removing Claude Code hooks on remote %s...\n", host)
		session, err := shim.NewSSHSession(host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to open SSH session for Claude hook cleanup: %v\n", err)
		} else {
			defer session.Close()
			if changed, err := shim.RemoveRemoteClaudeManagedHooks(session); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove managed Claude settings hooks: %v\n", err)
			} else if changed {
				fmt.Println("      managed hooks removed from ~/.claude/settings.json")
			}
			if err := shim.SetRemoteClaudeHooksEnabled(session, true); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove Claude no-hooks marker: %v\n", err)
			}
			if removed, err := shim.UninstallRemoteClaudeWrapperIfPresent(session); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to restore claude wrapper: %v\n", err)
			} else if removed {
				fmt.Println("      claude wrapper removed; original entry restored from sidecar")
			} else {
				fmt.Println("      no cc-clip claude wrapper installed")
			}
		}

		fmt.Printf("Removing PATH marker from remote %s...\n", host)
		if err := shim.RemoveRemotePath(host); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove PATH marker: %v\n", err)
		} else {
			fmt.Println("PATH marker removed from remote shell rc file.")
		}
	}
}

// shouldAbortUninstallCodexNewerSchema decides whether `uninstall --codex`
// must refuse to proceed because the remote was deployed by a newer cc-clip
// (deploy-state schema > this binary's). Extracted as a pure helper so the
// abort decision and its operator-facing message are unit-testable without a
// live SSH session. Returns (false, "") for a nil/legacy/current-schema state.
//
// Unlike the connect guard, there is intentionally NO --force escape hatch:
// uninstall is not an emergency-recovery path, so an older binary must never
// be allowed to tear down a newer-schema remote and clobber its deploy.json.
func shouldAbortUninstallCodexNewerSchema(host string, remoteState *shim.DeployState) (bool, string) {
	if !remoteState.IsNewerSchema() {
		return false, ""
	}
	return true, fmt.Sprintf(
		"remote %s was deployed by a newer cc-clip (deploy-state schema v%d > this binary's v%d); refusing to uninstall Codex from it and clobber its newer state. Upgrade this cc-clip first.",
		host, remoteState.SchemaVersion, shim.CurrentDeploySchemaVersion())
}

// cmdUninstallCodexRemote cleans up Codex support on a remote host via SSH.
func cmdUninstallCodexRemote(host string) {
	fmt.Printf("Uninstalling Codex support from %s...\n", host)

	session, err := shim.NewSSHSession(host)
	if err != nil {
		log.Fatalf("SSH connection failed: %v", err)
	}
	defer session.Close()

	// Forward downgrade guard (before ANY teardown): if the remote was deployed
	// by a newer cc-clip, refuse — tearing it down would rewrite deploy.json with
	// this older binary's schema and silently drop unknown newer fields. A read
	// error or nil/legacy state is NOT a newer schema, so normal uninstalls
	// proceed unaffected.
	if preState, perr := shim.ReadRemoteState(session); perr == nil {
		if abort, msg := shouldAbortUninstallCodexNewerSchema(host, preState); abort {
			log.Fatalf("      %s", msg)
		}
	}

	var teardownError bool
	var stateError bool

	// Step 1: Stop x11-bridge
	fmt.Println("[1/6] Stopping x11-bridge...")
	stopBridgeRemote(session)
	fmt.Println("      done")

	// Step 2: Stop Xvfb
	fmt.Println("[2/6] Stopping Xvfb...")
	if err := xvfb.StopRemote(session, codexStateDir); err != nil {
		fmt.Printf("      warning: %v\n", err)
		teardownError = true
	} else {
		fmt.Println("      done")
	}

	// Step 3: Remove codex state directory
	fmt.Println("[3/6] Removing codex state files...")
	if _, err := session.Exec(fmt.Sprintf("rm -rf %s", codexStateDir)); err != nil {
		fmt.Printf("      warning: could not remove codex state files: %v\n", err)
		teardownError = true
	} else {
		fmt.Println("      done")
	}

	// Step 4: Strip cc-clip notify block from ~/.codex/config.toml.
	// Without this, codex keeps trying to invoke a now-missing
	// cc-clip binary on every hook event.
	fmt.Println("[4/6] Stripping notify block from ~/.codex/config.toml...")
	if err := shim.StripRemoteCodexNotifyConfig(session); err != nil {
		fmt.Printf("      warning: %v\n", err)
		teardownError = true
	} else {
		fmt.Println("      done")
	}

	// Step 5: Remove DISPLAY marker
	fmt.Println("[5/6] Removing DISPLAY marker...")
	if err := shim.RemoveDisplayMarkerSession(session); err != nil {
		fmt.Printf("      warning: %v\n", err)
		teardownError = true
	} else {
		fmt.Println("      done")
	}

	// Step 6: Update deploy state
	fmt.Println("[6/6] Updating deploy state...")
	remoteState, err := shim.ReadRemoteState(session)
	if err != nil {
		fmt.Printf("      warning: could not read deploy state: %v\n", err)
		stateError = true
	} else if remoteState != nil {
		remoteState.Codex = nil
		if err := shim.WriteRemoteState(session, remoteState); err != nil {
			fmt.Printf("      warning: could not update deploy state: %v\n", err)
			stateError = true
		} else {
			fmt.Println("      codex block removed from deploy.json")
		}
	} else {
		fmt.Println("      no deploy state found (already clean)")
	}

	fmt.Println()
	// Reflect a completed Codex teardown locally even when deploy.json cleanup
	// only produced a warning; otherwise the sticky Codex flag can outlive a
	// successful remote uninstall.
	if !teardownError {
		clearHostCodex(host)
	}

	if teardownError || stateError {
		fmt.Println("Codex uninstall completed with warnings. Check issues above.")
		os.Exit(1)
	}
	fmt.Println("Codex support removed successfully.")
}

// cmdUninstallCodexLocal cleans up Codex support on the local machine.
func cmdUninstallCodexLocal() {
	fmt.Println("Uninstalling Codex support (local)...")

	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".cache", "cc-clip", "codex")

	// Stop bridge
	fmt.Println("[1/3] Stopping x11-bridge...")
	stopLocalProcess(filepath.Join(stateDir, "bridge.pid"), "cc-clip x11-bridge")

	// Stop Xvfb
	fmt.Println("[2/3] Stopping Xvfb...")
	stopLocalProcess(filepath.Join(stateDir, "xvfb.pid"), "Xvfb")

	// Remove state dir
	fmt.Println("[3/3] Removing state files...")
	os.RemoveAll(stateDir)

	fmt.Println("Codex support removed (local).")
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
		// Detect a daemon still running a pre-upgrade binary (#22 P2). An
		// in-place binary replacement does not restart the service, so the
		// running daemon can silently lag this binary.
		if h, err := tunnel.FetchHealth(addr, probeTimeout); err == nil {
			if notice := daemonVersionNotice(h.Version, version); notice != "" {
				fmt.Println(notice)
			}
		}
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
	} else if runtime.GOOS == "windows" {
		running, err := service.Status()
		if err == nil {
			if running {
				fmt.Println("service: running (task scheduler)")
			} else {
				fmt.Println("service: not running")
			}
		} else {
			fmt.Println("service: not installed")
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
		if runtime.GOOS == "windows" {
			fmt.Printf("Scheduled task created and running.\n")
			fmt.Printf("  task: %s\n", service.PlistPath())
		} else {
			fmt.Printf("Launchd service installed and loaded.\n")
			fmt.Printf("  plist: %s\n", service.PlistPath())
			fmt.Printf("  logs:  ~/Library/Logs/cc-clip.log\n")
		}

	case "uninstall":
		if err := service.Uninstall(); err != nil {
			log.Fatalf("service uninstall failed: %v", err)
		}
		if runtime.GOOS == "windows" {
			fmt.Println("Scheduled task removed.")
		} else {
			fmt.Println("Launchd service unloaded and removed.")
		}

	case "status":
		running, err := service.Status()
		if err != nil {
			log.Fatalf("service status check failed: %v", err)
		}
		if running {
			if runtime.GOOS == "windows" {
				fmt.Println("service: running (task scheduler)")
			} else {
				fmt.Println("service: running (launchd)")
			}
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

// cmdX11Bridge runs the X11 clipboard bridge daemon (internal command).
func cmdX11Bridge() {
	display := getFlag("display", os.Getenv("DISPLAY"))
	port := getPort()

	home, _ := os.UserHomeDir()
	tokenDir := filepath.Join(home, ".cache", "cc-clip")
	tokenFile := tokenDir + "/session.token"

	if display == "" {
		log.Fatal("x11-bridge: --display or DISPLAY env required")
	}

	bridge, err := x11bridge.New(display, port, tokenFile)
	if err != nil {
		log.Fatalf("x11-bridge: initialization failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals (SIGINT + SIGTERM on Unix, SIGINT on Windows).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, shutdownSignals()...)
	go func() {
		<-sigCh
		log.Printf("x11-bridge: received shutdown signal")
		cancel()
	}()

	if err := bridge.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("x11-bridge: %v", err)
	}
}
