package tunnelmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrServiceAlreadyRunning = errors.New("tunnel service supervisor is already running")

// RunService is the LaunchAgent entry point for experimental managed tunnels.
// Disabled specs win over stale/corrupt runtime. Known operator-action failures
// exit successfully so
// KeepAlive.SuccessfulExit=false does not turn them into launchd restart loops.
// An unexpected runtime/persistence failure remains an error for launchd.
func RunService(ctx context.Context, configs *ServiceStore, id string, logf func(string, ...any)) error {
	return runService(ctx, configs, id, logf, func(spec Spec, store *Store) *Supervisor {
		return NewSupervisor(spec, store, &Backend{Spec: spec})
	})
}

func runService(ctx context.Context, configs *ServiceStore, id string, logf func(string, ...any), newSupervisor func(Spec, *Store) *Supervisor) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	config, err := configs.Load(id)
	if err != nil {
		logf("tunnel service %s stopped: cannot load desired configuration: %v", id, err)
		return nil
	}
	if !config.Enabled {
		logf("tunnel service %s is disabled", id)
		return nil
	}
	store, err := configs.RuntimeStore(id)
	if err != nil {
		return err
	}
	// Inspect before taking the lock: lock creation must not repair an unsafe
	// runtime directory and thereby make invalid persisted state acceptable.
	if err := readPrivateDir(filepath.Dir(store.Path())); err != nil && !errors.Is(err, os.ErrNotExist) {
		logf("tunnel service %s stopped: %v", id, err)
		return nil
	}
	unlock, err := lockService(store.Path() + ".lock")
	if errors.Is(err, ErrServiceAlreadyRunning) {
		logf("tunnel service %s stopped: %v", id, err)
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	// Re-read after taking the lifetime lock, before starting any SSH child.
	config, err = configs.Load(id)
	if err != nil || !config.Enabled {
		logf("tunnel service %s stopped: desired configuration unavailable or disabled: %v", id, err)
		return nil
	}
	// Validate runtime before Run so malformed/newer state cannot cause a
	// permanent launchd loop. Recovery must be an explicit management action.
	_, err = configs.ReadRuntime(*config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logf("tunnel service %s stopped: %v", id, err)
		return nil
	}
	sup := newSupervisor(config.Spec, store)
	sup.Logf = logf
	// Desired state remains authoritative even if launchctl bootout fails.
	// Cancel probes and waits as well as the child on disable or invalidation.
	runCtx, cancel := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	desiredChanged := false
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				current, err := configs.Load(id)
				if err != nil || !current.Enabled || current.Spec != config.Spec {
					desiredChanged = true
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	logf("managed tunnel service %s starting for %s on port %d (state: %s)", id, config.Spec.Host, config.Spec.Port, store.Path())
	err = sup.Run(runCtx)
	cancel()
	<-watchDone
	if desiredChanged {
		logf("tunnel service %s stopped: desired configuration changed, disabled or unavailable", id)
	}
	if errors.Is(err, ErrCrashLoopOpen) || sup.State() == StateConfigError || sup.State() == StateIdentityMismatch {
		logf("tunnel service %s stopped; operator action required: %v; %s", id, err, ServiceLocationHint)
		return nil
	}
	if err != nil {
		return fmt.Errorf("tunnel service %s: %w", id, err)
	}
	logf("managed tunnel service %s stopped", id)
	return nil
}
