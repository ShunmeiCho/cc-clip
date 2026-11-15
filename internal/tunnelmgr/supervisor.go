package tunnelmgr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
)

const (
	defaultProbeInterval       = 60 * time.Second
	defaultProbeTimeout        = 15 * time.Second
	defaultBootstrapTimeout    = 15 * time.Second
	defaultStartingGate        = 15 * time.Second
	defaultLocalRetryInterval  = 15 * time.Second
	defaultRemoteRetryInterval = 15 * time.Second
	defaultAttentionInterval   = 5 * time.Minute
	defaultConflictInterval    = 45 * time.Second
	defaultStartBackoffBase    = time.Second
	defaultStartBackoffMax     = 5 * time.Minute
	defaultStableWindow        = 30 * time.Second
	defaultStopGrace           = 4 * time.Second
	defaultCrashLoopThreshold  = 5
)

var ErrCrashLoopOpen = errors.New("tunnel crash-loop circuit breaker is open; fix the cause and run with --reset")

// Supervisor manages one manually enabled tunnel. Function fields keep the
// local health boundary testable without replacing the SSH backend itself.
type Supervisor struct {
	Spec    Spec
	Store   *Store
	Backend *Backend
	Logf    func(format string, args ...any)

	ProbeInterval       time.Duration
	ProbeTimeout        time.Duration
	BootstrapTimeout    time.Duration
	StartingGate        time.Duration
	LocalRetryInterval  time.Duration
	RemoteRetryInterval time.Duration
	AttentionInterval   time.Duration
	ConflictInterval    time.Duration
	StartBackoffBase    time.Duration
	StartBackoffMax     time.Duration
	StableWindow        time.Duration
	StopGrace           time.Duration
	CrashLoopThreshold  int

	ProbeLocal         func(addr string, timeout time.Duration) error
	FetchLocalIdentity func(addr, bearerToken string, timeout time.Duration) (tunnel.IdentityInfo, error)
	ReadLocalToken     func() (string, error)

	mu            sync.Mutex
	state         State
	remote        tunnel.RemoteTunnelState
	lastErr       string
	failures      int
	retryFailures int
	healthySince  time.Time
	lastKey       string
	lastPersist   time.Time
}

func NewSupervisor(spec Spec, store *Store, backend *Backend) *Supervisor {
	return &Supervisor{
		Spec:                spec,
		Store:               store,
		Backend:             backend,
		ProbeInterval:       defaultProbeInterval,
		ProbeTimeout:        defaultProbeTimeout,
		BootstrapTimeout:    defaultBootstrapTimeout,
		StartingGate:        defaultStartingGate,
		LocalRetryInterval:  defaultLocalRetryInterval,
		RemoteRetryInterval: defaultRemoteRetryInterval,
		AttentionInterval:   defaultAttentionInterval,
		ConflictInterval:    defaultConflictInterval,
		StartBackoffBase:    defaultStartBackoffBase,
		StartBackoffMax:     defaultStartBackoffMax,
		StableWindow:        defaultStableWindow,
		StopGrace:           defaultStopGrace,
		CrashLoopThreshold:  defaultCrashLoopThreshold,
		ProbeLocal:          tunnel.ProbeHealth,
		FetchLocalIdentity:  tunnel.FetchIdentity,
		ReadLocalToken:      token.ReadTokenFile,
	}
}

func (s *Supervisor) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Supervisor) RemoteState() tunnel.RemoteTunnelState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remote
}

func (s *Supervisor) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *Supervisor) Run(ctx context.Context) error {
	rec, err := s.loadOrInitialize()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.failures = rec.Runtime.ConsecutiveStartFailures
	s.retryFailures = rec.Runtime.ConsecutiveStartFailures
	s.remote = rec.Runtime.RemoteState
	s.lastErr = rec.Runtime.LastError
	if rec.Runtime.State == StateCrashLoop {
		s.state = StateCrashLoop
		s.mu.Unlock()
		return ErrCrashLoopOpen
	}
	if rec.Runtime.State == StateStopped {
		s.failures = 0
		s.retryFailures = 0
	}
	s.state = StateStarting
	s.mu.Unlock()
	if err := s.persist(); err != nil {
		return err
	}

	for ctx.Err() == nil {
		if !s.Backend.Running() {
			if exit := s.Backend.ConsumeLastExit(); exit != nil {
				cause := classifyExit(exit)
				retry, err := s.recordStartFailureAndWait(ctx, cause, exit)
				if err != nil {
					return s.shutdown(err, false)
				}
				if !retry {
					break
				}
			}

			ready, state, reason := s.localReady()
			if !ready {
				if err := s.transition(state, "", reason); err != nil {
					return s.shutdown(err, false)
				}
				if state == StateConfigError || state == StateIdentityMismatch {
					return fmt.Errorf("local tunnel configuration: %s", reason)
				}
				if !sleepInterruptible(ctx, s.LocalRetryInterval) {
					break
				}
				continue
			}

			if err := s.transition(StateStarting, "", ""); err != nil {
				return err
			}
			if err := s.Backend.Start(); err != nil {
				retry, retryErr := s.recordStartFailureAndWait(ctx, StateReconnecting, &ExitInfo{Code: -1, StderrTail: err.Error()})
				if retryErr != nil {
					return retryErr
				}
				if !retry {
					break
				}
				continue
			}
			if err := s.Backend.WaitReady(ctx, s.BootstrapTimeout); err != nil {
				if ctx.Err() != nil {
					s.Backend.Stop(s.StopGrace)
					break
				}
				exit := s.Backend.ConsumeLastExit()
				s.Backend.Stop(s.StopGrace)
				stoppedExit := s.Backend.ConsumeLastExit()
				if exit == nil {
					exit = stoppedExit
				}
				if exit == nil {
					exit = &ExitInfo{Code: -1, StderrTail: err.Error()}
				}
				cause := classifyExit(exit)
				retry, err := s.recordStartFailureAndWait(ctx, cause, exit)
				if err != nil {
					return err
				}
				if !retry {
					break
				}
				continue
			}
		}

		if !s.Backend.Forwarded() {
			forwardCtx, cancel := context.WithTimeout(ctx, s.ProbeTimeout)
			err := s.Backend.Forward(forwardCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				if errors.Is(err, ErrPortConflict) && s.Backend.Running() {
					checkCtx, checkCancel := context.WithTimeout(ctx, s.ProbeTimeout)
					checkErr := s.Backend.Check(checkCtx)
					checkCancel()
					if checkErr == nil {
						if err := s.transition(StatePortConflict, "", err.Error()); err != nil {
							return s.shutdown(err, false)
						}
						if !sleepInterruptible(ctx, s.ConflictInterval) {
							break
						}
						continue
					}
					err = fmt.Errorf("forward conflict followed by failed master check: %w", checkErr)
				}
				s.Backend.Stop(s.StopGrace)
				_ = s.Backend.ConsumeLastExit()
				retry, retryErr := s.recordStartFailureAndWait(ctx, StateReconnecting, &ExitInfo{Code: -1, StderrTail: err.Error()})
				if retryErr != nil {
					return retryErr
				}
				if !retry {
					break
				}
				continue
			}
		}

		ready, state, reason := s.localReady()
		if !ready {
			if err := s.transition(state, "", reason); err != nil {
				return s.shutdown(err, false)
			}
			if state == StateConfigError || state == StateIdentityMismatch {
				return s.shutdown(fmt.Errorf("local tunnel configuration: %s", reason), false)
			}
			if !sleepInterruptible(ctx, s.LocalRetryInterval) {
				break
			}
			continue
		}

		remote, probeErr := s.Backend.ProbeRemote(ctx, s.ProbeTimeout)
		if ctx.Err() != nil {
			break
		}
		if probeErr != nil {
			checkCtx, checkCancel := context.WithTimeout(ctx, s.ProbeTimeout)
			checkErr := s.Backend.Check(checkCtx)
			checkCancel()
			if checkErr != nil {
				s.Backend.Stop(s.StopGrace)
				continue
			}
		}
		if probeErr != nil {
			if err := s.transition(StateRemoteUnknown, remote, probeErr.Error()); err != nil {
				return s.shutdown(err, false)
			}
		} else if !remote.Healthy() {
			if err := s.transition(stateForRemote(remote), remote, remote.Summary(s.Spec.Port)); err != nil {
				return s.shutdown(err, false)
			}
		} else {
			identityState, identity, identityErr := s.Backend.ProbeIdentity(ctx, s.ProbeTimeout)
			if ctx.Err() != nil {
				break
			}
			if identityErr != nil {
				checkCtx, checkCancel := context.WithTimeout(ctx, s.ProbeTimeout)
				checkErr := s.Backend.Check(checkCtx)
				checkCancel()
				if checkErr != nil {
					s.Backend.Stop(s.StopGrace)
					continue
				}
			}
			switch {
			case identityErr != nil:
				err = s.transition(StateRemoteUnknown, remote, identityErr.Error())
			case identityState == tunnel.RemoteIdentityHelperMissing:
				err = s.transition(StateIdentityHelperMissing, remote, "remote tunnel identity helper is missing or does not support tunnel probe-identity")
			case identityState == tunnel.RemoteIdentityEndpointUnavailable:
				err = s.transition(StateIdentityEndpointUnavailable, remote, "remote daemon tunnel identity endpoint unavailable")
			case identityState == tunnel.RemoteIdentityTokenInvalid:
				err = s.transition(StateRemoteTokenInvalid, remote, fmt.Sprintf(
					"remote token rejected by local daemon; run cc-clip connect %q --token-only from the local machine",
					s.Spec.Host,
				))
			case identityState != tunnel.RemoteIdentityOK:
				err = s.transition(StateRemoteUnknown, remote, "remote tunnel identity probe did not complete")
			case identity.InstanceID != s.Spec.ExpectedInstanceID:
				err = s.transition(StateIdentityMismatch, remote, "remote tunnel reached a different cc-clip instance")
				if err == nil {
					return s.shutdown(fmt.Errorf("remote tunnel identity mismatch"), false)
				}
			default:
				err = s.observeHealthy(remote)
			}
			if err != nil {
				return s.shutdown(err, false)
			}
		}

		if !sleepUntilProbeOrChildExit(ctx, s.nextProbeInterval(), s.Backend.Done()) {
			break
		}
	}
	return s.shutdown(nil, true)
}

func (s *Supervisor) loadOrInitialize() (*Record, error) {
	rec, err := s.Store.Load()
	if err == nil {
		if rec.Spec != s.Spec {
			return nil, fmt.Errorf("persisted tunnel spec differs from requested spec: %s; inspect the differences before explicitly retrying with --reset", DescribeSpecDifferences(rec.Spec, s.Spec))
		}
		return rec, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load tunnel state: %w (use --reset to reset managed-tunnel runtime)", err)
	}
	rec = &Record{
		SchemaVersion: schemaVersion,
		Spec:          s.Spec,
		Runtime:       Runtime{State: StateUnknown, UpdatedAt: time.Now().UTC()},
	}
	if err := s.Store.Save(rec); err != nil {
		return nil, fmt.Errorf("initialize tunnel state: %w", err)
	}
	return rec, nil
}

// localIdentityMismatchHint names the usual cause of a local identity
// mismatch (#182): on the remote host, the port is the forward to the local
// machine's daemon, so an instance that answers there is never this one.
const localIdentityMismatchHint = "the daemon answering on this port is not this machine's cc-clip daemon. " +
	"Run 'cc-clip tunnel run' on your local machine (the one running 'cc-clip serve'), not on the remote host, " +
	"where this port is the forward to your local daemon"

func (s *Supervisor) localReady() (bool, State, string) {
	addr := fmt.Sprintf("127.0.0.1:%d", s.Spec.Port)
	if err := s.ProbeLocal(addr, 2*time.Second); err != nil {
		return false, StateLocalDaemonDown, err.Error()
	}
	tok, err := s.ReadLocalToken()
	if err != nil {
		return false, StateConfigError, fmt.Sprintf("read local daemon token: %v", err)
	}
	identity, err := s.FetchLocalIdentity(addr, tok, 2*time.Second)
	if err != nil {
		if errors.Is(err, tunnel.ErrIdentityEndpointUnavailable) {
			return false, StateIdentityEndpointUnavailable, err.Error()
		}
		return false, StateConfigError, err.Error()
	}
	if identity.InstanceID != s.Spec.ExpectedInstanceID {
		return false, StateIdentityMismatch, "local daemon instance does not match persisted tunnel spec; " + localIdentityMismatchHint
	}
	return true, StateHealthy, ""
}

func stateForRemote(remote tunnel.RemoteTunnelState) State {
	switch remote {
	case tunnel.RemoteTunnelDown:
		return StateRemoteDown
	case tunnel.RemoteTunnelStale:
		return StateRemoteStale
	case tunnel.RemoteTunnelUnverified:
		return StateRemoteUnverified
	default:
		return StateRemoteUnknown
	}
}

func (s *Supervisor) recordStartFailure(cause State, exit *ExitInfo) error {
	s.mu.Lock()
	oldState := s.state
	s.healthySince = time.Time{}
	s.lastErr = describeStartFailure(exit)
	if cause == StateAuthRequired || cause == StateHostKeyError || cause == StateConfigError {
		s.state = cause
	} else {
		s.retryFailures++
		if exit.Uptime < s.StartingGate {
			s.failures++
		} else {
			s.failures = 0
		}
		if s.failures >= s.CrashLoopThreshold {
			s.state = StateCrashLoop
		} else {
			s.state = cause
		}
	}
	s.mu.Unlock()
	if err := s.persist(); err != nil {
		return err
	}
	s.logTransition(oldState)
	return nil
}

func describeStartFailure(exit *ExitInfo) string {
	if !exit.processExit {
		return exit.StderrTail // Backend errors contain no raw SSH output.
	}
	message := fmt.Sprintf("ssh child exited with code %d after %s", exit.Code, exit.Uptime.Round(time.Millisecond))
	// ProxyCommand and other user SSH helpers can print secrets. Use their
	// output only for classification; never persist or log the original text.
	switch classifyExit(exit) {
	case StateAuthRequired:
		return message + ": SSH authentication required"
	case StateHostKeyError:
		return message + ": SSH host key verification failed"
	case StateConfigError:
		return message + ": invalid SSH configuration; repair it before restarting"
	}
	return message
}

func (s *Supervisor) recordStartFailureAndWait(ctx context.Context, cause State, exit *ExitInfo) (bool, error) {
	if err := s.recordStartFailure(cause, exit); err != nil {
		return false, err
	}
	if s.State() == StateCrashLoop {
		return false, ErrCrashLoopOpen
	}
	if s.State() == StateConfigError {
		return false, errors.New("invalid SSH configuration; repair it before restarting")
	}
	return sleepInterruptible(ctx, s.retryDelay(cause)), nil
}

func (s *Supervisor) retryDelay(cause State) time.Duration {
	if cause == StateAuthRequired || cause == StateHostKeyError {
		return s.AttentionInterval
	}
	return s.backoff()
}

func (s *Supervisor) transition(state State, remote tunnel.RemoteTunnelState, lastErr string) error {
	s.mu.Lock()
	oldState := s.state
	if state != StateHealthy {
		s.healthySince = time.Time{}
	}
	s.state = state
	s.remote = remote
	s.lastErr = lastErr
	s.mu.Unlock()
	if err := s.persist(); err != nil {
		return err
	}
	s.logTransition(oldState)
	return nil
}

func (s *Supervisor) observeHealthy(remote tunnel.RemoteTunnelState) error {
	now := time.Now()
	s.mu.Lock()
	if s.state != StateHealthy || s.healthySince.IsZero() {
		s.healthySince = now
	}
	if now.Sub(s.healthySince) >= s.StableWindow {
		s.retryFailures = 0
	}
	s.mu.Unlock()
	return s.transition(StateHealthy, remote, "")
}

func (s *Supervisor) nextProbeInterval() time.Duration {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state == StateRemoteDown || state == StateRemoteStale {
		return s.RemoteRetryInterval
	}
	return s.ProbeInterval
}

func (s *Supervisor) logTransition(oldState State) {
	if s.Logf == nil {
		return
	}
	s.mu.Lock()
	state, lastErr := s.state, s.lastErr
	s.mu.Unlock()
	if oldState != state {
		s.Logf("managed tunnel state: %s", state)
		if state == StateRemoteTokenInvalid && lastErr != "" {
			s.Logf("managed tunnel recovery: %s", lastErr)
		}
	}
}

func (s *Supervisor) persist() error {
	s.mu.Lock()
	rec := &Record{
		SchemaVersion: schemaVersion,
		Spec:          s.Spec,
		Runtime: Runtime{
			State:                    s.state,
			RemoteState:              s.remote,
			LastError:                s.lastErr,
			ConsecutiveStartFailures: s.failures,
			UpdatedAt:                time.Now().UTC(),
		},
	}
	key := fmt.Sprintf("%s|%s|%s|%d", s.state, s.remote, s.lastErr, s.failures)
	if key == s.lastKey && time.Since(s.lastPersist) < s.ProbeInterval {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := s.Store.Save(rec); err != nil {
		return fmt.Errorf("persist tunnel state: %w", err)
	}
	s.mu.Lock()
	s.lastKey = key
	s.lastPersist = rec.Runtime.UpdatedAt
	s.mu.Unlock()
	return nil
}

func (s *Supervisor) shutdown(runErr error, stopped bool) error {
	s.Backend.Stop(s.StopGrace)
	if stopped {
		s.mu.Lock()
		s.state = StateStopped
		s.mu.Unlock()
		if err := s.persist(); err != nil && runErr == nil {
			runErr = err
		}
	}
	return runErr
}

func (s *Supervisor) backoff() time.Duration {
	s.mu.Lock()
	failures := s.retryFailures
	s.mu.Unlock()
	d := s.StartBackoffBase
	for i := 1; i < failures; i++ {
		next := d * 2
		if next >= s.StartBackoffMax {
			d = s.StartBackoffMax
			break
		}
		d = next
	}
	if d > s.StartBackoffMax {
		d = s.StartBackoffMax
	}
	jittered := time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
	if jittered > s.StartBackoffMax {
		return s.StartBackoffMax
	}
	return jittered
}

func classifyExit(exit *ExitInfo) State {
	if exit == nil {
		return StateReconnecting
	}
	msg := strings.ToLower(exit.StderrTail)
	if exit.Code == 255 {
		for _, diagnostic := range []string{"bad configuration option", "bad port", "bad forwarding specification", "unsupported option", "invalid command-line option", "terminating,", "no argument after keyword", "missing argument"} {
			if strings.Contains(msg, diagnostic) {
				return StateConfigError
			}
		}
		if strings.Contains(msg, "permission denied") {
			return StateAuthRequired
		}
		if strings.Contains(msg, "host key verification failed") || strings.Contains(msg, "remote host identification has changed") {
			return StateHostKeyError
		}
	}
	return StateReconnecting
}

func sleepInterruptible(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func sleepUntilProbeOrChildExit(ctx context.Context, d time.Duration, childDone <-chan struct{}) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-childDone:
		return true
	case <-t.C:
		return true
	}
}
