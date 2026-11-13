package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/instanceid"
	"github.com/shunmei/cc-clip/internal/service"
	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

type tunnelServiceOptions struct {
	host   string
	port   int
	follow bool
	lines  int
}

func parseTunnelServiceArgs(command string, args []string) (opts tunnelServiceOptions, err error) {
	opts.lines = 200
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--follow" || arg == "-f":
			if command != "logs" {
				return opts, fmt.Errorf("%s is only valid for logs", arg)
			}
			opts.follow = true
		case arg == "--port" || strings.HasPrefix(arg, "--port=") || arg == "--lines" || strings.HasPrefix(arg, "--lines=") || arg == "-n":
			name, value, hasValue := strings.Cut(arg, "=")
			isPort := name == "--port"
			if isPort && command != "enable" && command != "status" || !isPort && command != "logs" {
				return opts, fmt.Errorf("%s is not valid for %s", name, command)
			}
			if !hasValue {
				i++
				if i == len(args) {
					return opts, fmt.Errorf("%s requires a value", name)
				}
				value = args[i]
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || isPort && n > 65535 || !isPort && n > 10000 {
				return opts, fmt.Errorf("invalid %s value %q", name, value)
			}
			if isPort {
				if opts.port != 0 {
					return opts, errors.New("--port may only be specified once")
				}
				opts.port = n
			} else {
				opts.lines = n
			}
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag %s", arg)
		case opts.host == "" && arg != "":
			opts.host = arg
		default:
			return opts, errors.New("at most one <host> argument is allowed")
		}
	}
	if command != "status" && opts.host == "" {
		return opts, errors.New("a <host> argument is required")
	}
	if strings.ContainsAny(opts.host, "\x00\r\n") {
		return opts, errors.New("invalid SSH host")
	}
	return opts, nil
}

type tunnelServiceCommands struct {
	configs         *tunnelmgr.ServiceStore
	jobs            service.TunnelManager
	binary          string
	out             io.Writer
	localIdentity   func(int) (string, error)
	defaultPort     func() (int, error)
	interactiveAuth func(context.Context, string) error
	now             func() time.Time
}

func newTunnelServiceCommands(out io.Writer) (*tunnelServiceCommands, error) {
	jobs, err := service.NewTunnelManager()
	if err != nil {
		return nil, err
	}
	configs, err := tunnelmgr.DefaultServiceStore()
	if err != nil {
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &tunnelServiceCommands{configs: configs, jobs: jobs, binary: binary, out: out, localIdentity: tunnelLocalIdentity, defaultPort: tunnelServicePort, interactiveAuth: interactiveTunnelAuth, now: time.Now}, nil
}

func tunnelServicePort() (int, error) {
	if port, ok := service.InstalledPort(); ok {
		return port, nil
	}
	if value := os.Getenv("CC_CLIP_PORT"); value != "" {
		p, err := strconv.Atoi(value)
		if err != nil || p < 1 || p > 65535 {
			return 0, errors.New("CC_CLIP_PORT must be a port between 1 and 65535")
		}
		return p, nil
	}
	return 18339, nil
}

func tunnelLocalIdentity(port int) (string, error) {
	loaded, err := service.Status()
	if err != nil {
		return "", err
	}
	if !loaded {
		return "", errors.New("local daemon service is not loaded; on the local macOS machine run cc-clip service install")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := tunnel.ProbeHealth(addr, 2*time.Second); err != nil {
		return "", fmt.Errorf("local daemon on port %d is not healthy: %w; check cc-clip service status", port, err)
	}
	dir, err := token.TokenDir()
	if err != nil {
		return "", err
	}
	id, err := instanceid.Load(dir)
	if err != nil {
		return "", fmt.Errorf("read existing local instance identity: %w", err)
	}
	tok, err := token.ReadTokenFile()
	if err != nil {
		return "", fmt.Errorf("read local daemon token: %w", err)
	}
	identity, err := tunnel.FetchIdentity(addr, tok, 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("verify local daemon identity: %w", err)
	}
	if identity.InstanceID != id {
		return "", fmt.Errorf("local daemon identity mismatch: %s; requested identity is the daemon's observed instance; %s", tunnelmgr.DescribeSpecDifferences(tunnelmgr.Spec{ExpectedInstanceID: id}, tunnelmgr.Spec{ExpectedInstanceID: identity.InstanceID}), tunnelmgr.ServiceLocationHint)
	}
	return id, nil
}

func interactiveTunnelAuth(ctx context.Context, host string) error {
	// The terminal belongs to OpenSSH: cc-clip neither reads nor stores secrets.
	// A separate connection avoids accepting an already authenticated user master.
	args := []string{"-T", "-o", "BatchMode=no", "-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "StrictHostKeyChecking=ask", "-o", "ConnectTimeout=15", "--", host, "true"}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func tunnelServiceError(err error) error {
	if err == nil || strings.Contains(err.Error(), tunnelmgr.ServiceLocationHint) {
		return err
	}
	return fmt.Errorf("%w; %s", err, tunnelmgr.ServiceLocationHint)
}

func (c *tunnelServiceCommands) run(ctx context.Context, command string, opts tunnelServiceOptions) (runErr error) {
	defer func() { runErr = tunnelServiceError(runErr) }()
	if command == "status" {
		return c.status(opts)
	}
	if command == "logs" {
		return c.logs(ctx, opts)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	unlock, err := c.configs.LockManagement(lockCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("acquire tunnel management lock: %w", err)
	}
	defer unlock()
	switch command {
	case "enable":
		return c.enable(ctx, opts)
	case "restart":
		return c.restart(ctx, opts.host, false)
	case "disable":
		return c.disable(ctx, opts.host)
	case "auth":
		return c.restart(ctx, opts.host, true)
	default:
		return fmt.Errorf("unknown tunnel service command %s", command)
	}
}

func (c *tunnelServiceCommands) preflight(ctx context.Context, spec tunnelmgr.Spec) error {
	result, err := c.jobs.Preflight(ctx, c.binary, spec)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return errors.New("service-context authentication returned an unknown result")
	}
	switch result.State {
	case "ok":
		return nil
	case "auth-required":
		return fmt.Errorf("service-context SSH authentication failed; run cc-clip tunnel auth %s, then configure an unencrypted IdentityFile or a keychain/agent accessible to the LaunchAgent; terminal-only authentication is insufficient", spec.Host)
	case "host-key-error":
		return fmt.Errorf("service-context SSH host key verification failed; inspect known_hosts and run cc-clip tunnel auth %s to verify the host key in the foreground", spec.Host)
	default:
		return fmt.Errorf("service-context SSH check: %s; check SSH configuration/network and retry", result.State)
	}
}

func (c *tunnelServiceCommands) verifyLocal(spec tunnelmgr.Spec) error {
	id, err := c.localIdentity(spec.Port)
	if err != nil {
		return err
	}
	if id != spec.ExpectedInstanceID {
		local := spec
		local.ExpectedInstanceID = id
		return fmt.Errorf("persisted service identity differs from the local daemon: %s; requested identity is the daemon's observed instance; inspect identity and explicitly disable/re-enable", tunnelmgr.DescribeSpecDifferences(spec, local))
	}
	return nil
}

func (c *tunnelServiceCommands) enable(ctx context.Context, opts tunnelServiceOptions) error {
	id := tunnelmgr.TunnelID(opts.host)
	previous, err := c.configs.Load(id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var config tunnelmgr.ServiceConfig
	if previous != nil {
		config = *previous
		if previous.Enabled && opts.port != 0 && config.Spec.Port != opts.port {
			requested := config.Spec
			requested.Port = opts.port
			return fmt.Errorf("service configuration differs from request: %s; disable before explicitly reconfiguring", tunnelmgr.DescribeSpecDifferences(config.Spec, requested))
		}
		if previous.Enabled {
			if err := c.verifyLocal(config.Spec); err != nil {
				return err
			}
		} else {
			if opts.port != 0 {
				config.Spec.Port = opts.port
			}
			identity, err := c.localIdentity(config.Spec.Port)
			if err != nil {
				return err
			}
			config.Spec.ExpectedInstanceID = identity
			if err := config.Validate(); err != nil {
				return err
			}
		}
	} else {
		port := opts.port
		if port == 0 {
			port, err = c.defaultPort()
			if err != nil {
				return err
			}
		}
		identity, err := c.localIdentity(port)
		if err != nil {
			return err
		}
		config, err = tunnelmgr.NewServiceConfig(tunnelmgr.Spec{Host: opts.host, Port: port, ExpectedInstanceID: identity}, false)
		if err != nil {
			return err
		}
	}
	oldConfig := config
	if previous != nil {
		oldConfig = *previous
	}
	oldRuntime, runtimeErr := c.configs.ReadRuntime(oldConfig)
	if runtimeErr != nil && !errors.Is(runtimeErr, os.ErrNotExist) {
		return runtimeErr
	}
	if err := c.preflight(ctx, config.Spec); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if previous != nil && !previous.Enabled {
		if err := c.stop(ctx, id); err != nil {
			return err
		}
		// A disabled->enabled transition is an explicit retry/reconfigure.
		// Only a validated old runtime may be replaced with the new spec.
		store, err := c.configs.RuntimeStore(id)
		if err != nil {
			return err
		}
		if err := store.Reset(config.Spec); err != nil {
			return err
		}
	}
	config.Enabled = true
	if err := c.configs.Save(config); err != nil {
		if previous != nil && !previous.Enabled {
			return errors.Join(err, c.restoreRuntime(*previous, oldRuntime))
		}
		return err
	}
	if err := c.jobs.Install(c.binary, id); err != nil {
		// Failure restores previous desired state; newly created records remain
		// disabled for diagnosis. No SSH config or deployment records are touched.
		rollback := config
		rollback.Enabled = false
		if previous != nil {
			rollback = *previous
		}
		rollbackErr := c.configs.Save(rollback)
		var stopErr error
		if previous == nil || !previous.Enabled {
			stopErr = c.stop(ctx, id)
			rollbackErr = errors.Join(rollbackErr, stopErr)
		}
		if previous != nil && !previous.Enabled && stopErr == nil {
			rollbackErr = errors.Join(rollbackErr, c.restoreRuntime(*previous, oldRuntime))
		}
		return errors.Join(err, rollbackErr)
	}
	fmt.Fprintf(c.out, "Experimental tunnel service enabled for %s on port %d.\n", opts.host, config.Spec.Port)
	fmt.Fprintf(c.out, "SSH config is unchanged. Existing RemoteForward sessions may cause port-conflict; inspect cc-clip tunnel status %s.\n", opts.host)
	return nil
}

func (c *tunnelServiceCommands) restoreRuntime(previous tunnelmgr.ServiceConfig, record *tunnelmgr.Record) error {
	store, err := c.configs.RuntimeStore(previous.ID)
	if err != nil {
		return err
	}
	if record != nil {
		return store.Save(record)
	}
	return store.Reset(previous.Spec)
}

func (c *tunnelServiceCommands) stop(ctx context.Context, id string) error {
	if err := c.jobs.Stop(id); err != nil {
		return err
	}
	stopCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return c.configs.WaitStopped(stopCtx, id)
}

func (c *tunnelServiceCommands) restart(ctx context.Context, host string, authenticate bool) error {
	config, err := c.configs.Load(tunnelmgr.TunnelID(host))
	if errors.Is(err, os.ErrNotExist) && authenticate {
		// Allow first host-key/authentication setup before enable, without
		// persisting an enabled spec or installing the long-running job.
		if err := c.interactiveAuth(ctx, host); err != nil {
			return fmt.Errorf("interactive SSH authentication: %w", err)
		}
		port, err := c.defaultPort()
		if err != nil {
			return err
		}
		spec := tunnelmgr.Spec{Host: host, Port: port, ExpectedInstanceID: "authentication-only"}
		if err := c.preflight(ctx, spec); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "Service-context authentication verified for %s; run cc-clip tunnel enable %s to enable the service.\n", host, host)
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := c.configs.ReadRuntime(*config); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !config.Enabled && !authenticate {
		return fmt.Errorf("tunnel service is disabled; run cc-clip tunnel enable %s", host)
	}
	if authenticate {
		if err := c.interactiveAuth(ctx, host); err != nil {
			return fmt.Errorf("interactive SSH authentication: %w", err)
		}
	}
	if err := c.preflight(ctx, config.Spec); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !config.Enabled {
		fmt.Fprintf(c.out, "Authentication verified for %s; service remains disabled.\n", host)
		return nil
	}
	if err := c.verifyLocal(config.Spec); err != nil {
		return err
	}
	if err := c.stop(ctx, config.ID); err != nil {
		return err
	}
	if err := c.configs.ResetRuntime(*config); err != nil {
		return err
	}
	if err := c.jobs.Install(c.binary, config.ID); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Experimental tunnel service restarted for %s.\n", host)
	return nil
}

func (c *tunnelServiceCommands) disable(ctx context.Context, host string) error {
	id := tunnelmgr.TunnelID(host)
	config, err := c.configs.Load(id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if config != nil {
		config.Enabled = false
		if err := c.configs.Save(*config); err != nil {
			return err
		}
	}
	// Persist disabled first: a concurrent launchd start must not resurrect it.
	if err := c.stop(ctx, id); err != nil {
		return err
	}
	if err := c.jobs.Uninstall(id); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Tunnel service disabled for %s. SSH config is unchanged; diagnostic state and logs are retained.\n", host)
	return nil
}

func (c *tunnelServiceCommands) status(opts tunnelServiceOptions) error {
	fmt.Fprintf(c.out, "Service commands: %s.\n", tunnelmgr.ServiceLocationHint)
	ids, err := c.configs.ListIDs()
	if opts.host != "" {
		ids = []string{tunnelmgr.TunnelID(opts.host)}
		err = nil
	}
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintln(c.out, "No experimental tunnel services configured.")
		return nil
	}
	var failures []error
	for _, id := range ids {
		config, err := c.configs.Load(id)
		if errors.Is(err, os.ErrNotExist) && opts.host != "" {
			fmt.Fprintf(c.out, "%s: not configured; run cc-clip tunnel enable %s.\n", opts.host, opts.host)
			continue
		}
		if err != nil {
			fmt.Fprintf(c.out, "%s: configuration unknown: %v\n", id, err)
			failures = append(failures, err)
			continue
		}
		fmt.Fprintf(c.out, "%s (experimental, id %s)\n  desired: enabled=%t, port=%d\n", config.Spec.Host, id, config.Enabled, config.Spec.Port)
		if opts.port != 0 && opts.port != config.Spec.Port {
			requested := config.Spec
			requested.Port = opts.port
			fmt.Fprintf(c.out, "  configuration: %s; disable/re-enable to reconfigure explicitly\n", tunnelmgr.DescribeSpecDifferences(config.Spec, requested))
		}
		job, jobErr := c.jobs.Inspect(id)
		if jobErr != nil {
			fmt.Fprintf(c.out, "  job/process: unknown: %v\n", jobErr)
			failures = append(failures, jobErr)
		} else {
			fmt.Fprintf(c.out, "  job: registered=%t; supervisor: %s; pid=%d\n", job.Loaded, job.Process, job.PID)
			if job.LastExit != nil {
				fmt.Fprintf(c.out, "  last exit code: %d\n", *job.LastExit)
			}
		}
		rec, runtimeErr := c.configs.ReadRuntime(*config)
		state := tunnelmgr.StateUnknown
		if runtimeErr == nil {
			state = rec.Runtime.State
			fmt.Fprintf(c.out, "  last observed: %s (remote=%s, updated=%s)\n", state, rec.Runtime.RemoteState, rec.Runtime.UpdatedAt.Format(time.RFC3339))
			if rec.Runtime.LastError != "" {
				fmt.Fprintf(c.out, "  reason: %s\n", rec.Runtime.LastError)
			}
		} else if !errors.Is(runtimeErr, os.ErrNotExist) {
			fmt.Fprintf(c.out, "  runtime: unknown: %v\n", runtimeErr)
			failures = append(failures, runtimeErr)
		}
		health := "not healthy"
		if !config.Enabled {
			health = "disabled"
		} else if state == tunnelmgr.StateHealthy {
			age := c.now().Sub(rec.Runtime.UpdatedAt)
			if jobErr == nil && job.Loaded && job.Process == "running" && age >= 0 && age <= 2*time.Minute {
				health = "healthy (recent supervisor observation)"
			} else {
				health = "unknown (runtime is stale or supervisor is not proven running)"
			}
		}
		fmt.Fprintf(c.out, "  health: %s\n", health)
		remedyState := state
		if state == tunnelmgr.StateHealthy && strings.HasPrefix(health, "unknown") {
			remedyState = tunnelmgr.StateUnknown
		}
		fmt.Fprintf(c.out, "  next: %s\n", tunnelServiceRemedy(config.Spec.Host, remedyState, config.Enabled, jobErr != nil || !job.Loaded || job.Process != "running"))
	}
	return errors.Join(failures...)
}

func tunnelServiceRemedy(host string, state tunnelmgr.State, enabled, stopped bool) string {
	if !enabled {
		return "cc-clip tunnel enable " + host
	}
	switch state {
	case tunnelmgr.StateAuthRequired, tunnelmgr.StateHostKeyError:
		return "cc-clip tunnel auth " + host
	case tunnelmgr.StateRemoteTokenInvalid:
		return "cc-clip connect " + host + " --token-only"
	case tunnelmgr.StateLocalDaemonDown:
		return "cc-clip service status; check the daemon on the persisted port"
	case tunnelmgr.StateIdentityHelperMissing:
		return "cc-clip connect " + host + " --force"
	case tunnelmgr.StatePortConflict:
		return "release the existing forwarding session for this host, then let the supervisor retry; SSH config is not migrated automatically"
	case tunnelmgr.StateIdentityMismatch:
		return tunnelmgr.ServiceLocationHint + "; inspect cc-clip tunnel logs " + host + " and repair identity before restarting"
	case tunnelmgr.StateConfigError:
		return "inspect cc-clip tunnel logs " + host + " and repair configuration/identity before restarting"
	case tunnelmgr.StateCrashLoop:
		return "inspect cc-clip tunnel logs " + host + "; fix the cause, then cc-clip tunnel restart " + host
	}
	if stopped {
		return "cc-clip tunnel restart " + host
	}
	if state == tunnelmgr.StateHealthy {
		return "no action required if the recent observation remains valid"
	}
	return "cc-clip tunnel logs " + host + "; check network, remote health and identity"
}

func (c *tunnelServiceCommands) logs(ctx context.Context, opts tunnelServiceOptions) error {
	id := tunnelmgr.TunnelID(opts.host)
	if _, err := c.configs.Load(id); err != nil {
		return err
	}
	path, err := c.jobs.LogPath(id)
	if err != nil {
		return err
	}
	f, err := tunnelmgr.OpenPrivateFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if opts.lines < 1 || opts.lines > 10000 {
		return errors.New("log line count must be between 1 and 10000")
	}
	// Keep only the requested tail in memory. Existing logs are not truncated.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	tail := make([]string, opts.lines)
	count := 0
	for scanner.Scan() {
		tail[count%opts.lines] = scanner.Text()
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	start := count - opts.lines
	if start < 0 {
		start = 0
	}
	for i := start; i < count; i++ {
		if _, err := fmt.Fprintln(c.out, tail[i%opts.lines]); err != nil {
			return err
		}
	}
	if !opts.follow {
		return nil
	}
	// LaunchAgent logs append to the same file. Follow appended bytes until
	// Ctrl-C/SIGTERM; never change or replace the user's diagnostic file.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := io.Copy(c.out, f); err != nil {
				return err
			}
		}
	}
}

func cmdTunnelService(command string, args []string) {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			tunnelUsage(os.Stdout)
			return
		}
	}
	opts, err := parseTunnelServiceArgs(command, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip tunnel %s: %v\n", command, tunnelServiceError(err))
		os.Exit(2)
	}
	commands, err := newTunnelServiceCommands(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip tunnel %s: %v\n", command, tunnelServiceError(err))
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	if err := commands.run(ctx, command, opts); err != nil {
		fmt.Fprintf(os.Stderr, "cc-clip tunnel %s: %v\n", command, err)
		os.Exit(1)
	}
}

func cmdTunnelCheckAuth(args []string) {
	if len(args) != 4 || args[0] != "--id" || args[2] != "--directory" || !filepath.IsAbs(args[3]) {
		logAuthCheckError(errors.New("invalid service authentication arguments"))
		return
	}
	configs := tunnelmgr.NewServiceStoreAt(args[3])
	config, err := configs.Load(args[1])
	if err != nil {
		logAuthCheckError(err)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result := tunnelmgr.CheckAuthentication(ctx, config.Spec)
	data, err := json.Marshal(result)
	if err != nil {
		logAuthCheckError(err)
		return
	}
	tmp, err := os.CreateTemp(args[3], ".auth-result-*")
	if err != nil {
		logAuthCheckError(err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		logAuthCheckError(err)
		return
	}
	if err := tmp.Sync(); err != nil {
		logAuthCheckError(err)
		return
	}
	if err := tmp.Close(); err != nil {
		logAuthCheckError(err)
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(args[3], "auth-result.json")); err != nil {
		logAuthCheckError(err)
	}
}

func logAuthCheckError(err error) { fmt.Fprintf(os.Stderr, "cc-clip tunnel check-auth: %v\n", err) }
