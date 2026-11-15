package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/instanceid"
	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

// cmdTunnel implements `cc-clip tunnel <subcommand>`.
//
// The experimental managed tunnel accepts either a foreground host or an
// authoritative service config ID through run.
// Service management is explicit opt-in. Existing setup/update commands do not
// enable jobs or change the interactive RemoteForward workflow.
func cmdTunnel() {
	if len(os.Args) < 3 {
		tunnelUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[2] {
	case "run":
		cmdTunnelRun(os.Args[3:])
	case "enable", "status", "restart", "disable", "auth", "logs":
		cmdTunnelService(os.Args[2], os.Args[3:])
	case "check-auth":
		cmdTunnelCheckAuth(os.Args[3:])
	case "probe-identity":
		cmdTunnelProbeIdentity(os.Stdout)
	case "-h", "--help", "help":
		tunnelUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: tunnel %s\n", os.Args[2])
		tunnelUsage(os.Stderr)
		os.Exit(2)
	}
}

// cmdTunnelProbeIdentity is the remote capability used by the supervisor.
// It is intentionally omitted from user-facing help: callers deploy it via
// the existing connect flow and invoke it over the private SSH master.
func cmdTunnelProbeIdentity(w io.Writer) {
	state := tunnel.RemoteIdentityUnknown
	var identity tunnel.IdentityInfo
	tok, err := token.ReadTokenFile()
	if err != nil {
		state = tunnel.RemoteIdentityTokenInvalid
	} else {
		identity, err = tunnel.FetchIdentity(fmt.Sprintf("127.0.0.1:%d", getPort()), tok, 5*time.Second)
		switch {
		case err == nil:
			state = tunnel.RemoteIdentityOK
		case errors.Is(err, tunnel.ErrIdentityUnauthorized):
			state = tunnel.RemoteIdentityTokenInvalid
		case errors.Is(err, tunnel.ErrIdentityEndpointUnavailable):
			state = tunnel.RemoteIdentityEndpointUnavailable
		}
	}
	fmt.Fprintln(w, tunnel.RemoteIdentityProbeOutput(state, identity))
}

func tunnelUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: cc-clip tunnel <subcommand> [options] (experimental)
       cc-clip tunnel run <host> [--port N] [--reset]
       cc-clip tunnel run --id <tunnel-id>

Service commands (macOS only; run on the local machine running cc-clip serve):
  enable <host> [--port N]  Preflight authentication, persist config, start job
  status [<host>] [--port N]  Show desired state, job/process and observed health
  restart <host>           Validate and restart; clear valid crash-loop runtime
  disable <host>           Cancel persistent enablement and stop/remove the job
  auth <host>              Authenticate in the terminal, then verify in launchd
  logs <host> [-n N] [-f]  Show the last N lines (default 200); -f follows output

All managed tunnel entry points remain experimental. No service command edits
SSH config, migrates RemoteForward, or deploys remote hooks/helpers.
enable requires an installed, healthy local daemon. Background authentication
is verified by a temporary LaunchAgent, not by the current shell environment.
status --port N compares an explicit requested port without changing persisted
config or runtime. Differences name each field and its persisted/requested values,
for example: port: persisted=18339, requested=18340.
Runtime differences compare the saved runtime spec with the desired service spec;
status does not probe local identity or infer a requested port from CC_CLIP_PORT.

Supervisor entry points:
  run <host>         Run the managed tunnel supervisor for <host> in the
                     foreground (experimental, manual only). Run it on your
                     local machine (where cc-clip serve runs), not on <host>.
  run --id ID        Experimental LaunchAgent entry point. Reads an enabled spec
                     from ~/.cache/cc-clip/tunnel-specs/; ignores CC_CLIP_PORT.
                     Cannot be combined with <host>, --port or --reset.

The supervisor starts a private non-interactive ssh master
(BatchMode, ClearAllForwardings, ExitOnForwardFailure) that holds exactly
one reverse forward 127.0.0.1:<port> -> 127.0.0.1:<port>, probes the local
daemon's health through the forward from the remote side, and reconnects
with bounded backoff. It stops cleanly on Ctrl-C / SIGTERM.

run itself does not install a LaunchAgent or write ~/.ssh/config. ssh reads
the named Host normally so HostName, User, IdentityFile and ProxyJump keep
working. No existing command enables services automatically. Your interactive
RemoteForward setup remains supported; use tunnel disable to release a service's
managed forward (both modes bind the same remote port).
Any ssh to <host> that takes the RemoteForward from your SSH config, including
other tools' background connections, keeps the port first and leaves this
command in port-conflict. To let it own the port while you try it, comment
out that RemoteForward line for <host>, and restore it afterwards.

Before first use with an existing host, deploy the identity helper:
  cc-clip connect <host> --force

This experimental command invokes the identity helper only at
$HOME/.local/bin/cc-clip, so hosts deployed with --use-remote-bin are not
currently supported. Authenticated identity probes do not renew the token's
30-day sliding expiration; actual clipboard requests keep their normal renewal
behavior. If the remote token is rejected, the supervisor keeps its SSH forward
and reports remote-token-invalid. Run cc-clip connect <host> --token-only on the
local machine to sync a valid daemon token; the same forward recovers in place.

Foreground state is persisted under ~/.cache/cc-clip/tunnels/ (mode 0600).
Use --reset to explicitly rebuild corrupt foreground runtime or clear its
crash-loop state. Background runtime is separate, under tunnel-runtime/.
The --id entry point does not create specs or install jobs. Service specs and
diagnostic logs are retained on disable; restart never bypasses schema validation.`)
}

func parseTunnelRunArgs(args []string) (host string, reset bool, err error) {
	opts, err := parseTunnelRunOptions(args)
	return opts.host, opts.reset, err
}

type tunnelRunOptions struct {
	host  string
	id    string
	reset bool
}

func parseTunnelRunOptions(args []string) (opts tunnelRunOptions, err error) {
	portSet := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--id", strings.HasPrefix(args[i], "--id="):
			if opts.id != "" {
				return opts, errors.New("--id may only be specified once")
			}
			value := strings.TrimPrefix(args[i], "--id=")
			if args[i] == "--id" {
				i++
				if i == len(args) {
					return opts, errors.New("--id requires a tunnel id")
				}
				value = args[i]
			}
			if err := tunnelmgr.ValidateTunnelID(value); err != nil {
				return opts, err
			}
			opts.id = value
		case args[i] == "--port", strings.HasPrefix(args[i], "--port="):
			portSet = true
			value := strings.TrimPrefix(args[i], "--port=")
			if args[i] == "--port" {
				i++
				if i == len(args) {
					return opts, errors.New("--port requires a port")
				}
				value = args[i]
			}
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return opts, fmt.Errorf("invalid port %q", value)
			}
		case args[i] == "--reset":
			opts.reset = true
		case strings.HasPrefix(args[i], "-"):
			return opts, fmt.Errorf("unknown flag %s", args[i])
		case opts.host == "":
			opts.host = args[i]
		default:
			return opts, errors.New("exactly one <host> argument is required")
		}
	}
	if opts.id != "" && (opts.host != "" || opts.reset || portSet) {
		return opts, errors.New("--id cannot be combined with <host>, --port or --reset")
	}
	return opts, nil
}

func cmdTunnelRun(args []string) {
	opts, err := parseTunnelRunOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip tunnel run: %v\n", err)
		os.Exit(2)
	}
	if opts.host == "" && opts.id == "" {
		tunnelUsage(os.Stderr)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	if opts.id != "" {
		configs, err := tunnelmgr.DefaultServiceStore()
		if err != nil {
			log.Fatalf("cc-clip tunnel run: resolve service configuration: %v", err)
		}
		if err := tunnelmgr.RunService(ctx, configs, opts.id, log.Printf); err != nil {
			log.Fatalf("cc-clip tunnel run: %v", err)
		}
		return
	}
	host, reset := opts.host, opts.reset

	tokenDir, err := token.TokenDir()
	if err != nil {
		log.Fatalf("cc-clip tunnel run: resolve state directory: %v", err)
	}
	expectedInstanceID, err := instanceid.LoadOrCreate(tokenDir)
	if err != nil {
		log.Fatalf("cc-clip tunnel run: load instance identity: %v", err)
	}
	spec := tunnelmgr.Spec{Host: host, Port: getPort(), ExpectedInstanceID: expectedInstanceID}
	statePath, err := tunnelmgr.DefaultStorePath(host)
	if err != nil {
		log.Fatalf("cc-clip tunnel run: resolve state path: %v", err)
	}
	store := tunnelmgr.NewStoreAt(statePath)
	if reset {
		if err := store.Reset(spec); err != nil {
			log.Fatalf("cc-clip tunnel run: reset state: %v", err)
		}
	}
	backend := &tunnelmgr.Backend{Spec: spec}
	sup := tunnelmgr.NewSupervisor(spec, store, backend)
	sup.Logf = func(format string, args ...any) {
		log.Printf(format, args...)
	}

	log.Printf("managed tunnel starting for %s on port %d (state: %s)", host, spec.Port, statePath)
	if err := sup.Run(ctx); err != nil {
		if errors.Is(err, tunnelmgr.ErrCrashLoopOpen) {
			log.Printf("cc-clip tunnel run: %v", err)
			return
		}
		// A state file this build cannot validate is fail-closed: refuse to
		// run rather than guess. The file is named so the operator can
		// inspect it; nothing was started.
		log.Fatalf("cc-clip tunnel run: %v", err)
	}
	log.Printf("managed tunnel stopped")
}
