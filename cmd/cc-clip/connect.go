package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/service"
	"github.com/shunmei/cc-clip/internal/setup"
	"github.com/shunmei/cc-clip/internal/shim"
	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
	"github.com/shunmei/cc-clip/internal/xvfb"
)

type connectOpts struct {
	host         string
	port         int
	force        bool
	tokenOnly    bool
	useRemoteBin bool
	targets      DeployTargets // resolved deployment target set (parse + TTY menu); codex/claude/shim phases gate on its membership
	noNotify     bool
	// adoptForeignShim forwards the user's consent to the remote installer for
	// a shim path occupied by a file cc-clip did not write.
	adoptForeignShim bool
	noHooks          bool
	hooks            bool
	autoRecover      bool
}

// rejectAutoRecoverWithTokenOnly enforces the spec-mandated mutual
// exclusion between --auto-recover and --token-only at flag-parse time,
// before any SSH activity. Shared by cmdConnect and cmdSetup so the
// error message stays identical regardless of entrypoint.
//
// Returns silently when the combination is safe. Exits 2 with stderr
// guidance when the flags conflict.
func rejectAutoRecoverWithTokenOnly(cmdName string, autoRecover, tokenOnly bool) {
	if !autoRecover || !tokenOnly {
		return
	}
	fmt.Fprintf(os.Stderr, `error: --auto-recover cannot be combined with --token-only
       --auto-recover performs recovery and full reinstall.
       Re-run without --token-only:
           cc-clip %s <host> --auto-recover
       Or, if you only want to recover the binary without reinstalling the
       wrapper, run the manual recovery and then cc-clip connect --token-only:
           ssh <host> 'mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"'
           cc-clip connect <host> --token-only
`, cmdName)
	os.Exit(2)
}

func rejectHookControlWithTokenOnly(noHooks, hooks, tokenOnly bool) {
	if tokenOnly && (noHooks || hooks) {
		fmt.Fprintln(os.Stderr, `error: --no-hooks/--hooks cannot be combined with --token-only
       --token-only only syncs remote credentials and does not update remote
       Claude Code hook settings or markers.
       Re-run without --token-only:
           cc-clip connect <host> --no-hooks
       Or re-enable hook injection with:
           cc-clip connect <host> --hooks`)
		os.Exit(2)
	}
}

func rejectRemoteBinWithLocalBin(useRemoteBin bool, localBin string) {
	if !useRemoteBin || localBin == "" {
		return
	}
	fmt.Fprintln(os.Stderr, `error: --use-remote-bin cannot be combined with --local-bin
       --use-remote-bin uses the cc-clip executable already on the remote PATH,
       while --local-bin selects a local executable to upload.`)
	os.Exit(2)
}

// setupConnectOnlyFlags are flags only connect acts on. setup deploys through
// runConnect with host, port, targets, use-remote-bin and auto-recover alone,
// so these would otherwise be accepted and silently ignored. --token-only is
// included because setup reads it only to reject it next to --auto-recover.
var setupConnectOnlyFlags = []string{"force", "token-only", "adopt-foreign-shim", "no-hooks", "hooks", "no-notify"}

// setupRejectedConnectFlags names every connect-only flag present in setup's
// args, each with the connect command that honours it.
func setupRejectedConnectFlags(host string, args []string) error {
	var lines []string
	for _, name := range setupConnectOnlyFlags {
		if flagInArgs(args, name) {
			lines = append(lines, fmt.Sprintf("--%s is a connect flag; run \"cc-clip setup %s\" first, then \"cc-clip connect %s --%s\"", name, host, host, name))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return fmt.Errorf("error: %s", strings.Join(lines, "\n       "))
}

func cmdConnect() {
	if len(os.Args) < 3 {
		log.Fatal("usage: cc-clip connect <host> [--port PORT] [--force] [--token-only] [--use-remote-bin] [--no-notify] [--no-hooks|--hooks]")
	}
	host, err := hostFromArgs(os.Args[2:])
	if err != nil {
		log.Fatal("usage: cc-clip connect <host> [--port PORT] [--force] [--token-only] [--use-remote-bin] [--no-notify] [--no-hooks|--hooks]")
	}
	autoRecover := hasFlag("auto-recover")
	tokenOnly := hasFlag("token-only")
	useRemoteBin := hasFlag("use-remote-bin")
	noHooks := hasFlag("no-hooks")
	hooks := hasFlag("hooks")
	if noHooks && hooks {
		log.Fatal("usage: --no-hooks and --hooks are mutually exclusive")
	}
	rejectAutoRecoverWithTokenOnly("connect", autoRecover, tokenOnly)
	rejectHookControlWithTokenOnly(noHooks, hooks, tokenOnly)
	rejectRemoteBinWithLocalBin(useRemoteBin, getFlag("local-bin", ""))

	// Resolve deployment targets BEFORE any SSH/daemon activity so the
	// interactive menu (design §5) precedes the passphrase prompt. A multi-
	// target conflict or a non-Claude hook-control combination fails fast with
	// exit 2. On the non-TTY path resolveImplicitTargets falls back to {Claude}
	// only — an unattended run never silently selects --all / Xvfb / sudo
	// (constraint §6).
	targets, explicit, terr := parseDeployTargets(os.Args[2:])
	if terr != nil {
		fmt.Fprintln(os.Stderr, terr)
		os.Exit(2)
	}
	if !explicit {
		targets = resolveImplicitTargets(stdinIsTTY(), os.Stdin, os.Stdout, os.Stderr, DeployTargets{Claude: true}, "claude")
	}
	if herr := checkHookControlTargets(targets, noHooks, hooks); herr != nil {
		fmt.Fprintln(os.Stderr, herr)
		os.Exit(2)
	}
	maybeLegacyCodexNotice(os.Stderr, os.Args[2:], targets)

	runConnect(connectOpts{
		host:             host,
		port:             getPort(),
		force:            hasFlag("force"),
		tokenOnly:        tokenOnly,
		useRemoteBin:     useRemoteBin,
		targets:          targets,
		noNotify:         hasFlag("no-notify"),
		adoptForeignShim: hasFlag("adopt-foreign-shim"),
		noHooks:          noHooks,
		hooks:            hooks,
		autoRecover:      autoRecover,
	})
}

// decideN0SkipOnDetectError reports whether the N0 v0.7.0 gate may be safely
// skipped after DetectV070State returned an error. Skipping is safe ONLY when
// the remote has no existing claude install (claudeExists == false) AND that
// fact was determined reliably (probeErr == nil): with nothing installed, a
// v0.7.0 wrapper corruption is impossible. In every other case — claude is
// present, or its presence could not be probed — the gate fails closed because
// the remote cannot be proven uncorrupted.
func decideN0SkipOnDetectError(claudeExists bool, probeErr error) bool {
	return probeErr == nil && !claudeExists
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

	// N0: Pre-deploy v0.7.0 corruption detection. Runs before any other
	// remote write (including --token-only token sync) so a corrupted remote
	// either aborts cleanly or recovers in one step.
	//
	// Tri-state gate: NotCorrupted continues, Recoverable either aborts
	// with a hint or auto-recovers depending on --auto-recover, and
	// NonRecoverable always aborts (the real claude binary is lost; only
	// the operator can fix it by reinstalling via curl https://claude.ai/install.sh).
	fmt.Println("[N0] Checking for v0.7.0 wrapper corruption...")
	state, diag, err := shim.DetectV070State(session)
	if err != nil {
		// Detection could not run (rare; WrapRemoteShell hardens PATH). Skip only
		// when decideN0SkipOnDetectError proves the remote is fresh; otherwise
		// fail closed rather than guess. See that helper for the policy rationale.
		exists, probeErr := shim.RemoteClaudeProbe(session)
		if !decideN0SkipOnDetectError(exists, probeErr) {
			fmt.Fprintf(os.Stderr, `
error: N0 v0.7.0 detection could not run on remote (%v), and cc-clip could not
       confirm the remote has no existing claude install, so it will not
       continue — a present install might be in a corrupted v0.7.0 state.
       Ensure POSIX coreutils (readlink, head, grep, wc, tr) are available on
       the non-interactive SSH PATH, then retry:

    cc-clip connect %s
`, err, host)
			os.Exit(3)
		}
		fmt.Fprintf(os.Stderr, "      WARN: N0 detection could not run (%v); remote has no existing claude install — continuing\n", err)
		state = shim.V070NotCorrupted
		diag = "detection_skipped_fresh"
	}
	switch state {
	case shim.V070NotCorrupted:
		fmt.Printf("      no corruption detected (%s)\n", diag)
	case shim.V070Recoverable:
		if !opts.autoRecover {
			fmt.Fprintf(os.Stderr, `
error: detected v0.7.0 corruption on remote: ~/.local/bin/claude is a symlink
       to a file that is now a cc-clip wrapper, with the real binary backed up
       at ~/.local/bin/claude.cc-clip-bak.

To recover, either re-run with --auto-recover:

    cc-clip connect %s --auto-recover

Or fix manually:

    ssh %s 'mv ~/.local/bin/claude.cc-clip-bak "$(readlink -f ~/.local/bin/claude)"'
    cc-clip connect %s
`, host, host, host)
			os.Exit(3)
		}
		fmt.Println("      v0.7.0 corruption detected; running recovery...")
		if err := shim.RecoverV070Corruption(session); err != nil {
			log.Fatalf("      recovery failed: %v", err)
		}
		fmt.Println("      backup migrated to versions store; continuing install")
	case shim.V070NonRecoverable:
		// Fail-closed: do NOT continue, even with --auto-recover. The
		// recovery path requires a non-wrapper backup, which by definition
		// does not exist in this state. Proceeding would layer a fresh
		// wrapper on top of a half-broken Native Installer layout.
		fmt.Fprintf(os.Stderr, `
error: detected non-recoverable v0.7.0 corruption on remote (%s).
       ~/.local/bin/claude is a symlink whose target is a cc-clip wrapper,
       but the real-binary backup at ~/.local/bin/claude.cc-clip-bak is
       missing, too small, or itself a wrapper — auto-recovery cannot
       restore the original Claude Code binary.

Manual recovery required:

    1. Reinstall Claude Code on %s:

       ssh %s 'curl -fsSL https://claude.ai/install.sh | bash'

    2. After Claude Code is reinstalled, retry cc-clip:

       cc-clip connect %s

The --auto-recover flag does NOT help here because there is no usable
backup to migrate. cc-clip will refuse to write any wrapper until the
remote has a valid claude binary installed.
`, diag, host, host, host)
		os.Exit(3)
	}

	remoteBin := "~/.local/bin/cc-clip"
	var existingRemoteBin *shim.RemoteBinaryInfo
	if opts.useRemoteBin {
		existingRemoteBin, err = shim.InspectRemoteBinary(session)
		if err != nil {
			log.Fatalf("      failed to resolve remote binary: %v", err)
		}
		remoteBin = existingRemoteBin.Command()
		localVersion := normalizeVersion(version)
		remoteVersion := normalizeVersion(existingRemoteBin.Version)
		if localVersion != "" && remoteVersion != "" && localVersion != remoteVersion {
			log.Printf(
				"      warning: local version is %s, remote version is %s; continuing with the remote binary",
				localVersion,
				remoteVersion,
			)
		}
	}

	// --token-only: skip binary/shim, just sync token and verify tunnel
	if tokenOnly {
		if existingRemoteBin != nil {
			fmt.Printf(
				"[3/7] Using remote binary %s (%s)\n",
				existingRemoteBin.Path,
				existingRemoteBin.Version,
			)
			fmt.Println("[4/7] Skipping binary upload (--use-remote-bin)")
		} else {
			fmt.Println("[3/7] Skipping binary check (--token-only)")
			fmt.Println("[4/7] Skipping binary upload (--token-only)")
		}
		fmt.Println("[5/7] Skipping shim install (--token-only)")

		fmt.Printf("[6/7] Syncing token and session...\n")
		if err := shim.WriteRemoteTokenViaSession(session, daemonToken); err != nil {
			log.Fatalf("      failed to write token: %v", err)
		}
		fmt.Println("      token synced from local daemon")

		if sid, genErr := shim.GenerateSessionID(); genErr != nil {
			log.Printf("      warning: failed to generate session ID: %v", genErr)
		} else if writeErr := shim.WriteRemoteSessionID(session, sid); writeErr != nil {
			log.Printf("      warning: failed to write session ID: %v", writeErr)
		} else {
			fmt.Printf("      session ID: %s\n", sid[:16])
		}

		if !opts.noNotify {
			fmt.Println("      syncing notification nonce")
			notifyNonce, err := syncNotificationNonce(session, port, daemonToken, host)
			if err != nil {
				log.Printf("      warning: failed to sync notification nonce: %v", err)
			} else if err := runNotificationHealthProbe(port, notifyNonce); err != nil {
				log.Printf("      warning: notification health probe failed: %v", err)
			} else {
				fmt.Println("      notification nonce synced")
			}
		}

		connectVerifyTunnel(session, port, host, opts.targets, remoteBin, opts.noNotify)

		// Record this host even on the --token-only path so `hosts list` and
		// per-host update reminders reflect the most recent successful sync.
		// Codex flag is sticky in the registry, so passing codexTargeted here
		// (false for a plain --token-only run that resolves to {Claude}) won't
		// downgrade a previously recorded Codex=true entry.
		recordHostConnect(host, deployedRegistryVersion(existingRemoteBin), codexTargeted(opts.targets))
		return
	}

	// Step 3: Read remote deploy state and detect arch
	fmt.Printf("[3/7] Checking remote state...\n")
	remoteState, err := shim.ReadRemoteState(session)
	if err != nil {
		if force {
			log.Printf("      warning: could not read remote state; ignoring because --force is set: %v", err)
			remoteState = nil
		} else {
			log.Fatalf("      failed to read remote state: %v\n      Re-run with --force only if you intend to ignore deploy.json.", err)
		}
	}
	// Captured before --force nulls remoteState below: package-managed mode
	// adoption and the bridge-restart hash comparison both need the state as
	// it was actually read — under --force especially, since `cc-clip update`
	// prints `connect <host> --force` as its redeploy reminder.
	priorState := remoteState
	// Forward downgrade guard: if the remote was deployed by a newer cc-clip
	// (deploy-state schema > this binary's), refuse to overwrite it unless the
	// operator explicitly passes --force. Runs before any deploy-state or
	// binary write.
	if remoteState.IsNewerSchema() {
		if force {
			log.Printf("      warning: remote %s was deployed by a newer cc-clip (deploy-state schema v%d > this binary's v%d); --force DISCARDS the newer remote's deploy-state fields and rewrites deploy.json with this older binary's schema v%d (data loss) — upgrade this cc-clip instead to preserve them.",
				host, remoteState.SchemaVersion, shim.CurrentDeploySchemaVersion(), shim.CurrentDeploySchemaVersion())
		} else {
			log.Fatalf("      remote %s was deployed by a newer cc-clip (deploy-state schema v%d > this binary's v%d); refusing to overwrite it.\n      Upgrade this cc-clip, or pass --force to override.",
				host, remoteState.SchemaVersion, shim.CurrentDeploySchemaVersion())
		}
	}
	if remoteState != nil && !force {
		fmt.Printf("      remote state: binary=%s shim=%v\n", remoteState.BinaryVersion, remoteState.ShimInstalled)
	} else if force {
		fmt.Println("      --force: ignoring remote state")
		remoteState = nil
	} else {
		fmt.Println("      no previous deploy state")
	}

	// A host recorded as package-managed keeps that mode when the flag is
	// omitted; --local-bin is the explicit way back to uploaded deploys.
	if shouldAdoptRemoteBinMode(opts.useRemoteBin, getFlag("local-bin", ""), priorState) {
		fmt.Println("      deploy state records this host as package-managed; keeping the remote binary")
		fmt.Println("      (pass --local-bin to switch back to uploaded deploys)")
		existingRemoteBin, err = shim.InspectRemoteBinary(session)
		if err != nil {
			log.Fatalf("      failed to resolve remote binary: %v", err)
		}
		remoteBin = existingRemoteBin.Command()
	}

	// Step 4: Prepare and upload binary (skip if hash matches)
	var localBin string
	var needsUpload bool
	if existingRemoteBin != nil {
		fmt.Printf(
			"      remote binary: %s (%s)\n",
			existingRemoteBin.Path,
			existingRemoteBin.Version,
		)
		fmt.Println("[4/7] Using existing remote binary, skipping upload")
	} else {
		remoteOS, remoteArch, err := shim.DetectRemoteArchViaSession(session)
		if err != nil {
			log.Fatalf("      failed to detect remote arch: %v", err)
		}
		fmt.Printf("      %s/%s\n", remoteOS, remoteArch)

		localBin, err = prepareBinaryLocal(host, remoteOS, remoteArch)
		if err != nil {
			log.Fatalf("[4/7] Prepare binary failed: %v", err)
		}

		needsUpload = force || shim.NeedsUpload(localBin, remoteState)
		if !needsUpload {
			// Verify the remote binary actually exists — deploy state can be stale.
			if _, err := session.Exec(fmt.Sprintf("test -x %s", remoteBin)); err != nil {
				fmt.Println("[4/7] Remote binary missing despite cached state, re-uploading")
				needsUpload = true
			}
		}
		if needsUpload {
			fmt.Printf("[4/7] Uploading cc-clip binary...\n")
			// Stop bridge if running — it holds the binary open, preventing
			// overwrite. Record what it was serving first: when THIS run does
			// not target Codex, nothing downstream restarts it, and a
			// `connect --claude` on a Codex host silently killed Ctrl+V there.
			stoppedBridge, restartStopped := "", false
			if !codexTargeted(opts.targets) {
				if args, ok := runningBridgeArgs(session); ok {
					stoppedBridge, restartStopped = args, true
				}
			}
			stopBridgeRemote(session)
			// Ensure remote directory exists
			if _, err := session.Exec("mkdir -p ~/.local/bin"); err != nil {
				log.Fatalf("      failed to create remote binary directory: %v", err)
			}
			if err := shim.UploadBinaryViaSession(session, localBin, remoteBin); err != nil {
				log.Fatalf("      failed: %v", err)
			}
			fmt.Printf("      uploaded to %s\n", remoteBin)
			if restartStopped {
				display, bridgePort, ok := bridgeDisplayPort(stoppedBridge)
				switch {
				case !ok:
					failBridgeRestore(session, remoteState,
						"an x11-bridge was stopped for the upload but its display/port could not be read")
				default:
					if err := startBridgeRemote(session, display, bridgePort, remoteBin); err != nil {
						failBridgeRestore(session, remoteState,
							fmt.Sprintf("the x11-bridge stopped for the upload could not be restarted: %v", err))
					}
					fmt.Printf("      restarted x11-bridge on DISPLAY=:%s\n", display)
				}
			}
		} else {
			fmt.Println("[4/7] Binary up to date, skipping upload")
		}
	}

	// Step 5: Install shim — only for targets that use the clipboard shim
	// (Claude / opencode). Pure --codex / --agy read X11 directly or are
	// notify-only and need no shim, so install is SKIPPED; an existing shim is
	// NEVER uninstalled here (design §3 + Option A: --codex no longer installs
	// the Claude shim, but must not remove one a prior run left behind).
	var installOut string
	var needsShim bool
	if shimTargeted(opts.targets) {
		needsShim = force || shim.NeedsShimInstall(remoteState, port)
		if !needsShim {
			// Verify the shim file actually exists — cached state can be stale.
			shimTarget := "xclip"
			if remoteState != nil && remoteState.ShimTarget != "" {
				shimTarget = remoteState.ShimTarget
			}
			checkCmd := shimPresenceCheck("~/.local/bin/" + shimTarget)
			if _, err := session.Exec(checkCmd); err != nil {
				fmt.Println("      shim missing despite cached state, will reinstall")
				needsShim = true
			}
		}
		if needsShim {
			fmt.Printf("[5/7] Installing shim...\n")
			installCmd := remoteInstallCommand(remoteBin, port, opts.adoptForeignShim)
			out, err := session.Exec(installCmd)
			if err != nil {
				// Shim might already exist, try uninstall then install
				if uninstallOut, uninstallErr := session.Exec(fmt.Sprintf("%s uninstall 2>&1", remoteBin)); uninstallErr != nil {
					log.Printf("      warning: cleanup before install retry failed: %s: %v", uninstallOut, uninstallErr)
				}
				out, err = session.Exec(installCmd)
				if err != nil {
					// The remote refusal already names the file and the flag;
					// say where the flag goes from this side so the user does
					// not have to work out that connect forwards it.
					if !opts.adoptForeignShim && strings.Contains(out, shim.AdoptFlagHint) {
						log.Fatalf("      remote install refused to overwrite a file it did not write:\n%s\n\n"+
							"      Re-run and cc-clip will move it aside instead of destroying it:\n"+
							"        cc-clip connect %s %s", out, host, shim.AdoptFlagHint)
					}
					log.Fatalf("      remote install failed: %s: %v", out, err)
				}
			}
			installOut = out
			fmt.Printf("      %s\n", out)
		} else {
			fmt.Println("[5/7] Shim already installed, skipping")
		}
	} else {
		fmt.Println("[5/7] Skipping shim install (target needs no clipboard shim)")
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

	// Step 6: Sync token and session ID
	fmt.Printf("[6/7] Syncing token and session...\n")
	if err := shim.WriteRemoteTokenViaSession(session, daemonToken); err != nil {
		log.Fatalf("      failed to write token: %v", err)
	}
	fmt.Println("      token synced from local daemon")

	sessionID, err := shim.GenerateSessionID()
	if err != nil {
		log.Printf("      warning: failed to generate session ID: %v", err)
	} else {
		if err := shim.WriteRemoteSessionID(session, sessionID); err != nil {
			log.Printf("      warning: failed to write session ID: %v", err)
		} else {
			fmt.Printf("      session ID: %s\n", sessionID[:16])
		}
	}

	// Determine actual shim target from install output or prior state.
	shimTarget := "xclip"
	if needsShim {
		// Parse install output: it prints "Installed shim: <target>"
		if strings.Contains(installOut, "wl-paste") {
			shimTarget = "wl-paste"
		}
	} else if remoteState != nil && remoteState.ShimTarget != "" {
		shimTarget = remoteState.ShimTarget
	}
	var newState *shim.DeployState
	if existingRemoteBin != nil {
		newState = newDeployStateFromBinary(
			existingRemoteBin.Hash,
			existingRemoteBin.Version,
			shimTarget,
			pathFixed,
			remoteState,
			opts.targets,
			port,
		)
	} else {
		newState, err = newDeployState(
			localBin,
			version,
			shimTarget,
			pathFixed,
			remoteState,
			opts.targets,
			port,
		)
		if err != nil {
			log.Fatalf("      failed to prepare remote deploy state: %v", err)
		}
	}
	// Record the binary-ownership mode so the next flag-less connect (notably
	// the update reminder's `connect <host> --force`) keeps it. An explicit
	// --local-bin deploy lands in the upload arm and clears it.
	newState.UseRemoteBin = existingRemoteBin != nil
	if err := shim.WriteRemoteState(session, newState); err != nil {
		log.Printf("      warning: could not write remote deploy state: %v", err)
	}

	// Step 7: Verify tunnel
	connectVerifyTunnel(session, port, host, opts.targets, remoteBin, opts.noNotify)

	// Notification bridge setup (unless --no-notify)
	if !opts.noNotify {
		connectNotifySetup(session, port, daemonToken, host, newState, opts)
		if err := shim.WriteRemoteState(session, newState); err != nil {
			log.Printf("      warning: could not write remote deploy state: %v", err)
		}
	}

	// Steps 8-11: Codex support (only if Codex is among the resolved targets).
	// On the remote-bin path needsUpload is permanently false, so the bridge
	// restart decision also compares the remote executable's hash against the
	// prior state — a package-manager upgrade must restart the bridge.
	if codexTargeted(opts.targets) {
		codexOk := runConnectCodex(session, opts, needsUpload || remoteBinChanged(existingRemoteBin, priorState), newState, remoteBin)
		if err := shim.WriteRemoteState(session, newState); err != nil {
			log.Printf("      warning: could not update deploy state: %v", err)
		}
		if !codexOk {
			fmt.Println()
			// Only claim the shim is ready when this run actually targeted it;
			// pure --codex skips shim install (Step 5), so "Claude shim is ready"
			// would be inaccurate there.
			if shimTargeted(opts.targets) {
				fmt.Println("Claude shim is ready, but Codex support failed.")
			} else {
				fmt.Println("Codex support failed.")
			}
			fmt.Println("Fix the issues above and re-run: cc-clip connect", host, "--codex")
			os.Exit(1)
		}
	}

	// Record this host in the local registry so `cc-clip update` / `hosts list`
	// can surface it later. Only reached when every earlier step succeeded
	// (any error above exits via log.Fatal / os.Exit). Codex flag is sticky
	// inside the registry, so a plain connect won't downgrade a previously
	// recorded Codex=true.
	recordHostConnect(host, deployedRegistryVersion(existingRemoteBin), codexTargeted(opts.targets))
}

// deployedRegistryVersion returns the version to record for a host: the REMOTE
// executable's version when the host is package-managed, the local binary's
// otherwise. Recording the local version for a remote-bin host made
// `hosts list` show whatever this machine happened to build (and suppressed
// the redeploy reminder), while deploy.json said something else.
func deployedRegistryVersion(existingRemoteBin *shim.RemoteBinaryInfo) string {
	if existingRemoteBin != nil {
		return normalizeVersion(existingRemoteBin.Version)
	}
	return registryVersionOrEmpty()
}

func newDeployState(localBin, binaryVersion, shimTarget string, pathFixed bool, remoteState *shim.DeployState, targets DeployTargets, port int) (*shim.DeployState, error) {
	localHash, err := shim.LocalBinaryHash(localBin)
	if err != nil {
		return nil, err
	}

	return newDeployStateFromBinary(
		localHash,
		binaryVersion,
		shimTarget,
		pathFixed,
		remoteState,
		targets,
		port,
	), nil
}

func newDeployStateFromBinary(binaryHash, binaryVersion, shimTarget string, pathFixed bool, remoteState *shim.DeployState, targets DeployTargets, port int) *shim.DeployState {
	state := &shim.DeployState{
		BinaryHash:    binaryHash,
		BinaryVersion: binaryVersion,
		// Only claim a shim when this run targeted it (Claude/opencode). A
		// shim-less target (pure --codex/--agy) preserves any prior shim below
		// and never fabricates one on a fresh host.
		ShimInstalled: shimTargeted(targets),
		ShimTarget:    shimTarget,
		PathFixed:     pathFixed,
	}
	// Record WHICH shim this host now carries, so a later release can tell
	// that its template or port has moved on and reinstall exactly once.
	if state.ShimInstalled {
		state.ShimFingerprint = shim.TemplateFingerprint()
		state.ShimPort = port
	}
	if remoteState != nil {
		state.Notify = remoteState.Notify
		state.ClaudeWrapper = remoteState.ClaudeWrapper
		// Preserve existing Codex transport state when this run did not target Codex.
		if remoteState.Codex != nil && !codexTargeted(targets) {
			state.Codex = remoteState.Codex
		}
		// Preserve an existing shim when this run did not target it (pure
		// --codex/--agy): never downgrade or overwrite a shim we did not touch.
		if !shimTargeted(targets) {
			state.ShimInstalled = remoteState.ShimInstalled
			if remoteState.ShimTarget != "" {
				state.ShimTarget = remoteState.ShimTarget
			}
			state.ShimFingerprint = remoteState.ShimFingerprint
			state.ShimPort = remoteState.ShimPort
		}
	}
	return state
}

func cmdSetup() {
	if len(os.Args) < 3 {
		log.Fatal("usage: cc-clip setup <host> [--port PORT] [--text off|read|write|both]")
	}
	host, err := hostFromArgs(os.Args[2:])
	if err != nil {
		log.Fatal("usage: cc-clip setup <host> [--port PORT] [--text off|read|write|both]")
	}
	port := getPort()

	// Reject conflicting flag combinations at parse time, before any
	// dependency check or remote activity. Spec scenario 22 requires
	// setup to fail-fast just like connect does.
	autoRecover := hasFlag("auto-recover")
	tokenOnly := hasFlag("token-only")
	useRemoteBin := hasFlag("use-remote-bin")
	rejectAutoRecoverWithTokenOnly("setup", autoRecover, tokenOnly)
	rejectRemoteBinWithLocalBin(useRemoteBin, getFlag("local-bin", ""))
	if err := setupRejectedConnectFlags(host, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// Resolve deployment targets BEFORE any local dependency / daemon / SSH
	// activity so the interactive menu (design §5) precedes any prompt, and a
	// multi-target conflict fails fast with exit 2. setup defaults to {Claude}
	// (design §4/§12: no-sudo contract) on the non-TTY path. setup exposes no
	// --no-hooks/--hooks flags, so hook-control validation stays connect-only.
	targets, explicit, terr := parseDeployTargets(os.Args[2:])
	if terr != nil {
		fmt.Fprintln(os.Stderr, terr)
		os.Exit(2)
	}
	if !explicit {
		targets = resolveImplicitTargets(stdinIsTTY(), os.Stdin, os.Stdout, os.Stderr, DeployTargets{Claude: true}, "claude")
	}
	maybeLegacyCodexNotice(os.Stderr, os.Args[2:], targets)

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
		if textModeExplicit() && getTextMode() != installedTextMode() {
			fmt.Printf("      warning: --text=%s not applied to the running daemon (installed: %s);\n", getTextMode(), installedTextMode())
			fmt.Printf("      run: cc-clip service install --text=%s\n", getTextMode())
		}
	} else if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("      cannot determine executable path: %v", err)
		}
		exePath, _ = filepath.EvalSymlinks(exePath)
		if err := service.Install(exePath, port, textModeServeArgs(serviceTextMode())...); err != nil {
			log.Fatalf("      service install failed: %v", err)
		}
		if runtime.GOOS == "darwin" {
			fmt.Println("      launchd service installed and started")
		} else {
			fmt.Println("      scheduled task installed and started")
		}
		// Wait for daemon to be ready
		time.Sleep(500 * time.Millisecond)
	} else {
		log.Fatal("      daemon not running. Start it first: cc-clip serve")
	}

	// Step 4: Deploy to remote
	fmt.Printf("\n[4/4] Deploying to %s...\n", host)
	runConnect(connectOpts{
		host:         host,
		port:         port,
		targets:      targets,
		useRemoteBin: useRemoteBin,
		autoRecover:  autoRecover,
	})
}

// connectSuccessSummary returns the post-connect summary line, tailored to the
// resolved targets. The clipboard shim serves Claude Code / opencode; Codex
// prints its own readiness line from runConnectCodex, so a Codex-only run must
// not claim the Claude shim is ready (it is deliberately not installed under
// pure --codex).
func connectSuccessSummary(t DeployTargets, noNotify bool) string {
	switch {
	case t.Claude:
		return "Setup complete. Ctrl+V in remote Claude Code will paste images from your local clipboard."
	case t.Opencode:
		return "Setup complete. Ctrl+V in remote opencode will paste images from your local clipboard."
	case t.Codex:
		return "Setup complete. Codex CLI clipboard support is configured below."
	case t.Antigravity:
		if noNotify {
			return "Setup complete. Antigravity notifications skipped (--no-notify); clipboard transport is pending."
		}
		return "Setup complete. Antigravity notifications configured; clipboard transport is pending."
	case t.Cursor:
		return "Setup complete. Ctrl+V in remote Cursor will paste images once DISPLAY is set (see the note above)."
	default:
		return "Setup complete."
	}
}

// cursorDisplayNotice is printed when the Cursor target is selected. Cursor's
// clipboard reader only builds its xclip/wl-paste candidate list when DISPLAY
// or WAYLAND_DISPLAY is set in the shell it runs in; on a headless SSH session
// neither is, so the shim is never invoked at all. cc-clip deliberately does
// NOT inject a DISPLAY (issue #109, option C): a value with no X server behind
// it would leak into the whole login shell and turn the shim's real-xclip
// fallback into a guaranteed failure for every other consumer.
//
// The timeout note exists because Cursor kills each clipboard child after ~4s
// while the shim's default fetch timeout is 5s — a large image over a slow
// tunnel would be killed mid-transfer without the override.
const cursorDisplayNotice = `      Cursor CLI notes:
      1. Cursor only reads the clipboard when DISPLAY or WAYLAND_DISPLAY is set
         in the shell where it runs. Check with:  echo $DISPLAY
         If empty, connect with 'ssh -X <host>' or export an existing display.
         cc-clip does not set one for you.
      2. Cursor stops waiting for clipboard helpers after about 4 seconds; the
         shim's default fetch timeout is 5. For large images over a slow link,
         add to your remote shell rc:  export CC_CLIP_FETCH_TIMEOUT_MS=3000`

// maybePrintCursorNotice writes cursorDisplayNotice to out when the Cursor
// integration is among the resolved targets.
func maybePrintCursorNotice(out io.Writer, t DeployTargets) {
	if cursorTargeted(t) {
		fmt.Fprintln(out)
		fmt.Fprintln(out, cursorDisplayNotice)
	}
}

// connectVerifyTunnel verifies the SSH tunnel from the remote side.
func connectVerifyTunnel(session *shim.SSHSession, port int, host string, targets DeployTargets, remoteBin string, noNotify bool) {
	fmt.Printf("[7/7] Verifying tunnel from remote...\n")
	// Ask the daemon to identify itself through the forward rather than only
	// completing a TCP handshake. A stale sshd from a previous session keeps
	// the port in LISTEN, which satisfied the old /dev/tcp probe and made
	// connect print "tunnel verified" for a tunnel that carried nothing.
	probeOut, probeErr := session.Exec(tunnel.RemoteHealthProbeCommand(port))

	if probeErr != nil {
		// The probe itself could not run — the SSH master is dead, not the
		// tunnel. Surfacing the generic "tunnel not detected" guidance here
		// would send the user chasing a RemoteForward problem that does not
		// exist; name the real failure instead.
		fmt.Printf("      tunnel probe could not be executed over SSH: %v\n", probeErr)
		fmt.Println("      The SSH session appears to be down; re-run 'cc-clip connect' once SSH is reachable.")
	} else {
		state := tunnel.ClassifyRemoteProbeOutput(probeOut)
		var sessions []tunnel.SSHDSession
		if state == tunnel.RemoteTunnelStale {
			// Best-effort: list the user's sshd sessions so the holder can be
			// found without root (#173). A failure only drops the list.
			if out, err := session.Exec(tunnel.RemoteSSHDSessionsCommand()); err == nil {
				sessions = tunnel.ParseSSHDSessions(out)
			}
		}
		for _, line := range tunnelVerificationReport(state, port, host, sessions) {
			fmt.Println(line)
		}
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

	// The DISPLAY prerequisite must precede the summary: the summary's Cursor
	// line refers to "the note above".
	maybePrintCursorNotice(os.Stdout, targets)

	fmt.Println()
	fmt.Println(connectSuccessSummary(targets, noNotify))
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

	fmt.Printf("      cross-compiling cc-clip %s from %s\n", version, describeCrossCompileSource(srcDir))
	tmpBin := filepath.Join(os.TempDir(), fmt.Sprintf("cc-clip-%s-%s", remoteOS, remoteArch))
	buildCmd := exec.Command("go", crossCompileArgs(tmpBin, version)...)
	buildCmd.Dir = srcDir
	buildCmd.Env = append(os.Environ(), "GOOS="+remoteOS, "GOARCH="+remoteArch)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("cross-compile failed: %s: %w", string(out), err)
	}
	return tmpBin, nil
}

// Release downloads fail on a stall, not on a slow link: curl aborts when the
// transfer stays under releaseDownloadStallFloor bytes/s for
// releaseDownloadStallWindow, and never runs past updateDownloadTotal, the
// budget cc-clip update gives the same archive (#174). An unreachable host
// still fails within releaseDownloadConnectTimeout.
const (
	releaseDownloadConnectTimeout = 15 * time.Second
	releaseDownloadStallFloor     = 1024 // bytes per second
	releaseDownloadStallWindow    = 30 * time.Second
)

func releaseDownloadArgs(dest, url string) []string {
	return []string{
		"-fsSL",
		"--connect-timeout", strconv.Itoa(int(releaseDownloadConnectTimeout.Seconds())),
		"--speed-limit", strconv.Itoa(releaseDownloadStallFloor),
		"--speed-time", strconv.Itoa(int(releaseDownloadStallWindow.Seconds())),
		"--max-time", strconv.Itoa(int(updateDownloadTotal.Seconds())),
		"-o", dest, url,
	}
}

// describeCrossCompileSource names the tree the cross-compile fallback builds
// from. findSourceDir takes the first go.mod near the executable or in the
// current directory, so a checkout on another branch would otherwise ship
// unreleased code under the local version without any hint (#174).
func describeCrossCompileSource(dir string) string {
	commit, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return formatCrossCompileSource(dir, "", false)
	}
	status, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output()
	modified := err == nil && buildTreeModified(string(status))
	return formatCrossCompileSource(dir, strings.TrimSpace(string(commit)), modified)
}

// buildTreeModified reports whether `git status --porcelain` output shows a
// change that reaches the built binary: any tracked change, or an untracked
// file go build would pick up (*.go, go.mod, go.sum). Unrelated untracked
// files such as notes stay out of the warning.
func buildTreeModified(porcelain string) bool {
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		if !strings.HasPrefix(line, "??") {
			return true
		}
		path := strings.Trim(strings.TrimSpace(line[3:]), `"`)
		base := filepath.Base(path)
		if strings.HasSuffix(path, ".go") || base == "go.mod" || base == "go.sum" {
			return true
		}
	}
	return false
}

func formatCrossCompileSource(dir, commit string, modified bool) string {
	switch {
	case commit == "":
		return dir
	case modified:
		return fmt.Sprintf("%s (commit %s, with uncommitted changes)", dir, commit)
	default:
		return fmt.Sprintf("%s (commit %s)", dir, commit)
	}
}

func crossCompileArgs(outputPath, buildVersion string) []string {
	ldflags := fmt.Sprintf("-s -w -X main.version=%s", buildVersion)
	return []string{"build", "-ldflags", ldflags, "-o", outputPath, "./cmd/cc-clip/"}
}

// releaseVersion extracts the base release version from a git describe string.
// "0.3.0-1-g99b1298" → "0.3.0", "0.3.0" → "0.3.0".
// git describe format: <tag>-<N>-g<hash> where N = commits after tag.
func releaseVersion(ver string) string {
	// Split by "-" and check for the git describe pattern: at least 3 parts
	// where the last part starts with "g" (commit hash) and second-to-last is a number.
	parts := strings.Split(ver, "-")
	if len(parts) >= 3 {
		hash := parts[len(parts)-1]
		count := parts[len(parts)-2]
		if strings.HasPrefix(hash, "g") && isNumeric(count) {
			return strings.Join(parts[:len(parts)-2], "-")
		}
	}
	return ver
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func downloadReleaseBinary(targetOS, targetArch string) (string, error) {
	if version == "dev" {
		return "", fmt.Errorf("running dev build, no release version to download")
	}

	// Strip "v" prefix, then extract base release version from git describe output.
	// e.g. "v0.3.0-1-g99b1298" → "0.3.0-1-g99b1298" → "0.3.0"
	ver := releaseVersion(strings.TrimPrefix(version, "v"))
	archiveName := fmt.Sprintf("cc-clip_%s_%s_%s.tar.gz", ver, targetOS, targetArch)
	url := fmt.Sprintf("https://github.com/ShunmeiCho/cc-clip/releases/download/v%s/%s", ver, archiveName)

	tmpDir, err := os.MkdirTemp("", "cc-clip-download-*")
	if err != nil {
		return "", err
	}

	archivePath := filepath.Join(tmpDir, archiveName)
	dlCmd := exec.Command("curl", releaseDownloadArgs(archivePath, url)...)
	if out, err := dlCmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("download failed (%s): %s", url, string(out))
	}

	// Verify the archive's sha256 against the release checksums.txt before
	// extracting. This downloads a release binary and pushes it to a remote
	// host, so an unverified archive is a supply-chain exposure. On any
	// mismatch or missing entry, refuse to extract.
	checksumsURL := fmt.Sprintf("https://github.com/ShunmeiCho/cc-clip/releases/download/v%s/checksums.txt", ver)
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	csCmd := exec.Command("curl", releaseDownloadArgs(checksumsPath, checksumsURL)...)
	if out, err := csCmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("checksums download failed (%s): %s", checksumsURL, string(out))
	}
	checksumsContent, err := os.ReadFile(checksumsPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("read checksums.txt: %w", err)
	}
	if err := verifyArchiveChecksum(archivePath, string(checksumsContent), archiveName); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("checksum verification failed: %w", err)
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

// verifyArchiveChecksum computes the sha256 of the file at archivePath and
// compares it against the expected digest for archiveName as listed in a
// goreleaser checksums.txt. The checksums format is "<sha256>  <filename>"
// lines (two spaces). Returns an error on a missing entry or a mismatch so the
// caller can refuse to use an unverified archive.
func verifyArchiveChecksum(archivePath, checksumsContent, archiveName string) error {
	expected := ""
	for _, line := range strings.Split(checksumsContent, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == archiveName {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum for %s not found in checksums.txt", archiveName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))

	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", archiveName, expected, actual)
	}
	return nil
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

// --- Codex support ---

const codexStateDir = "~/.cache/cc-clip/codex"

// runConnectCodex executes steps 8-11 of the Codex deploy flow.
// Returns true on success, false on failure (Claude path is preserved).
func runConnectCodex(session *shim.SSHSession, opts connectOpts, binaryChanged bool, state *shim.DeployState, remoteBin string) bool {
	port := opts.port

	if opts.tokenOnly {
		fmt.Println("[8/11] Skipping Codex setup (--token-only)")
		fmt.Println("[9/11] Skipping (--token-only)")
		fmt.Println("[10/11] Skipping (--token-only)")
		fmt.Println("[11/11] Skipping (--token-only)")
		return true
	}

	// Step 8: Codex preflight
	fmt.Println("[8/11] Codex preflight...")
	if err := xvfb.CheckAvailable(session); err != nil {
		fmt.Println("      Xvfb not found, attempting auto-install...")
		if installErr := xvfb.TryInstall(session); installErr != nil {
			fmt.Printf("      auto-install failed: %v\n", installErr)
			fmt.Println("      Install Xvfb manually:")
			fmt.Println("        Debian/Ubuntu: sudo apt install xvfb")
			fmt.Println("        RHEL/Fedora:   sudo dnf install xorg-x11-server-Xvfb")
			return false
		}
		fmt.Println("      Xvfb auto-installed")
	} else {
		fmt.Println("      Xvfb available")
	}
	if _, err := session.Exec(fmt.Sprintf("mkdir -p %s", codexStateDir)); err != nil {
		fmt.Printf("      failed to create Codex state dir: %v\n", err)
		return false
	}

	// --force: tear down both bridge and Xvfb so they restart fresh.
	// This handles port changes, display drift, and stale state.
	if opts.force {
		fmt.Println("      --force: stopping existing Codex runtime")
		stopBridgeRemote(session)
		if err := xvfb.StopRemote(session, codexStateDir); err != nil {
			fmt.Printf("      warning: could not stop existing Xvfb: %v\n", err)
		}
	}

	// Step 9: Start or reuse Xvfb
	fmt.Println("[9/11] Starting Xvfb...")
	xvfbState, err := xvfb.StartRemote(session, codexStateDir)
	if err != nil {
		fmt.Printf("      Xvfb start failed: %v\n", err)
		dumpRemoteLog(session, codexStateDir+"/xvfb.log")
		return false
	}
	fmt.Printf("      Xvfb running on DISPLAY=:%s (PID %d)\n", xvfbState.Display, xvfbState.PID)

	// Step 10: Start or reuse x11-bridge
	fmt.Println("[10/11] Starting x11-bridge...")

	// Unconditionally restart the bridge when the binary changed (uploaded, or
	// a package-managed remote binary's hash moved) or --force was used.
	needsBridgeRestart := binaryChanged || opts.force
	if needsBridgeRestart {
		stopBridgeRemote(session)
	}

	if !needsBridgeRestart && isBridgeHealthy(session, xvfbState.Display, port) {
		fmt.Println("      x11-bridge already running, reusing")
	} else {
		// Stop any existing bridge first.
		stopBridgeRemote(session)

		if err := startBridgeRemote(session, xvfbState.Display, port, remoteBin); err != nil {
			fmt.Printf("      x11-bridge start failed: %v\n", err)
			dumpRemoteLog(session, codexStateDir+"/bridge.log")
			return false
		}
		fmt.Println("      x11-bridge started")
	}

	// Step 11: Inject DISPLAY marker + update state
	fmt.Println("[11/11] Injecting DISPLAY marker...")
	displayFixed := false
	if err := shim.FixDisplaySession(session); err != nil {
		fmt.Printf("      DISPLAY marker injection failed: %v\n", err)
		return false
	}
	displayFixed = true
	fmt.Println("      DISPLAY marker injected")

	state.Codex = &shim.CodexDeployState{
		Enabled:      true,
		Mode:         "x11-bridge",
		DisplayFixed: displayFixed,
	}

	fmt.Println()
	fmt.Println("Codex support ready. Open a new SSH shell and Ctrl+V will work in Codex CLI.")
	return true
}

// startBridgeRemote starts the x11-bridge daemon on the remote.
//
// XAUTHORITY is part of the environment because the display is started with
// -auth: an unauthenticated client is refused, which is the point.
func startBridgeRemote(session *shim.SSHSession, display string, port int, remoteBin string) error {
	startScript := fmt.Sprintf(
		`nohup env DISPLAY=":%s" XAUTHORITY=%s %s x11-bridge --display ":%s" --port %d > %s/bridge.log 2>&1 < /dev/null &
echo $! > %s/bridge.pid
sleep 0.3
kill -0 $(cat %s/bridge.pid 2>/dev/null) 2>/dev/null && echo 'bridge:ok' || echo 'bridge:fail'`,
		display, codexAuthEnvPath(), remoteBin, display, port,
		codexStateDir, codexStateDir, codexStateDir,
	)
	out, err := session.Exec(startScript)
	if err != nil {
		return fmt.Errorf("bridge start command failed: %w", err)
	}
	if strings.Contains(out, "bridge:fail") {
		return fmt.Errorf("bridge process died immediately after start")
	}
	return nil
}

// remoteInstallCommand is the remote `cc-clip install` invocation connect runs.
//
// It folds stderr into stdout because SSHSession.Exec captures stdout only,
// and the remote install reports a refusal through log.Fatalf on stderr: the
// refusal text, including the --adopt-foreign-shim hint connect checks for,
// never reached connect, and users saw "remote install failed: : exit status 1".
// A successful install writes only to stdout, so the output connect parses on
// success is unchanged.
func remoteInstallCommand(remoteBin string, port int, adoptForeign bool) string {
	cmd := fmt.Sprintf("%s install --port %d", remoteBin, port)
	if adoptForeign {
		cmd += " " + shim.AdoptFlagHint
	}
	return cmd + " 2>&1"
}

// shimPresenceCheck is the remote command that reports whether a cc-clip shim
// is installed at shimPath.
//
// It reads the file HEADER, not just line 1: the shim's first line is the #!
// and the ownership marker sits on line 2, so a `head -1 | grep cc-clip` probe
// never matched and every connect reinstalled the shim it had just verified.
//
// It applies the installer's own ownership rule (shim.ShimOwnerMarker within
// shim.ShimHeaderBytes, never a symlink): a bare "cc-clip" substring accepted a
// user's wrapper that merely mentions cc-clip, and connect then skipped
// installing the shim over a file the installer itself would refuse to own.
func shimPresenceCheck(shimPath string) string {
	return fmt.Sprintf("test -f %s && ! test -L %s && head -c %d %s | grep -qF '%s'",
		shimPath, shimPath, shim.ShimHeaderBytes, shimPath, shim.ShimOwnerMarker)
}

// failBridgeRestore ends the deploy when a bridge this run stopped could not be
// brought back.
//
// Warning and carrying on reported a successful deployment while leaving
// deploy.json claiming Codex was enabled and Ctrl+V dead on the remote. The
// recorded state is corrected first so the next run does not trust a
// capability that is no longer there.
func failBridgeRestore(session *shim.SSHSession, remoteState *shim.DeployState, reason string) {
	if remoteState != nil && remoteState.Codex != nil && remoteState.Codex.Enabled {
		corrected := *remoteState.Codex
		corrected.Enabled = false
		remoteState.Codex = &corrected
		if err := shim.WriteRemoteState(session, remoteState); err != nil {
			log.Printf("      warning: could not record the stopped bridge in deploy state: %v", err)
		}
	}
	log.Fatalf("      %s.\n"+
		"      Codex clipboard support is DOWN on this host and the deploy state now says so.\n"+
		"      Bring it back with: cc-clip connect <host> --codex", reason)
}

// codexAuthEnvPath is the X authority file written for `env VAR=value`, where a
// leading "~" is NOT expanded by every shell. Elsewhere the path appears at the
// start of a word, where tilde expansion is portable.
func codexAuthEnvPath() string {
	return `"$HOME` + strings.TrimPrefix(xvfb.AuthFilePath(codexStateDir), "~") + `"`
}

// readPidArgsShell sets $args to the argv of process $pid, space-joined.
//
// It reads /proc rather than `ps -p "$pid" -o args=`: busybox ps rejects -p, so
// on busybox remotes the bridge was never recognized — stop left it running and
// the upload path never restored it. The remote is always Linux, where
// /proc/<pid>/cmdline needs no userland tool.
const readPidArgsShell = `args=$(tr '\000' ' ' < "/proc/$pid/cmdline" 2>/dev/null)`

// stopBridgeRemote stops the x11-bridge on the remote (safe: verifies command).
func stopBridgeRemote(session *shim.SSHSession) {
	stopScript := fmt.Sprintf(
		`pid=$(cat %s/bridge.pid 2>/dev/null) && \
[ -n "$pid" ] && \
`+readPidArgsShell+` && \
case "$args" in *'cc-clip x11-bridge'*) true ;; *) false ;; esac && \
kill "$pid" 2>/dev/null && \
sleep 0.5 && \
kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null; \
rm -f %s/bridge.pid; true`,
		codexStateDir, codexStateDir,
	)
	// The script ends in `true`, so a non-nil error here means the SSH
	// transport itself failed (not the kill). Log it non-fatally so a dead
	// session does not silently leave a stale bridge running.
	if _, err := session.Exec(stopScript); err != nil {
		log.Printf("      warning: could not stop x11-bridge over SSH: %v", err)
	}
}

// runningBridgeArgs returns the argv of the x11-bridge this host has running.
// Verifies both PID liveness and command name to avoid false positives from
// stale PID files whose PID was reused by an unrelated process.
func runningBridgeArgs(session *shim.SSHSession) (string, bool) {
	const begin = "__CC_CLIP_BRIDGE_ARGS__"
	checkScript := fmt.Sprintf(
		`pid=$(cat %s/bridge.pid 2>/dev/null) && \
[ -n "$pid" ] && \
kill -0 "$pid" 2>/dev/null && \
`+readPidArgsShell+` && \
case "$args" in *'cc-clip x11-bridge'*) printf '%s%%s\n' "$args" ;; esac; true`,
		codexStateDir, begin,
	)
	out, err := session.Exec(checkScript)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, begin); i >= 0 {
			return strings.TrimSpace(line[i+len(begin):]), true
		}
	}
	return "", false
}

// bridgeDisplayPort reads the display and daemon port a running bridge was
// started with, straight off its argv.
func bridgeDisplayPort(args string) (string, int, bool) {
	fields := strings.Fields(args)
	var display string
	port := -1
	for i := 0; i+1 < len(fields); i++ {
		switch fields[i] {
		case "--display":
			display = strings.TrimPrefix(fields[i+1], ":")
		case "--port":
			if p, err := strconv.Atoi(fields[i+1]); err == nil {
				port = p
			}
		}
	}
	if display == "" || port < 0 {
		return "", 0, false
	}
	return display, port, true
}

// isBridgeHealthy reports whether the running bridge is one this connect can
// reuse: alive, ours, AND already serving the display and daemon port this run
// resolved. A PID-liveness-only check happily kept a bridge bound to the
// previous --port, so `connect --codex --port <new>` silently kept talking to
// the old daemon.
func isBridgeHealthy(session *shim.SSHSession, display string, port int) bool {
	args, ok := runningBridgeArgs(session)
	if !ok {
		return false
	}
	gotDisplay, gotPort, ok := bridgeDisplayPort(args)
	return ok && gotDisplay == display && gotPort == port
}

// dumpRemoteLog prints the last 20 lines of a remote log file.
func dumpRemoteLog(session *shim.SSHSession, logPath string) {
	out, err := session.Exec(fmt.Sprintf("tail -20 %s 2>/dev/null", logPath))
	if err == nil && out != "" {
		fmt.Println("      --- log ---")
		for _, line := range strings.Split(out, "\n") {
			fmt.Printf("      %s\n", line)
		}
		fmt.Println("      --- end ---")
	}
}
