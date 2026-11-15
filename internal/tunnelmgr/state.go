// Package tunnelmgr implements the managed persistent tunnel core from
// issue #108 Phase 1A: a minimal state store, a managed non-interactive SSH
// backend, and a single-tunnel supervisor that can be driven manually.
//
// The same supervisor supports foreground runs and the per-host LaunchAgent
// entry point, using independent desired service specs and runtime snapshots.
// No enable/disable CLI, SSH config migration or rollback is implemented here;
// the legacy interactive RemoteForward workflow remains the default.
//
// Health semantics are built on the v0.9.2 probe primitives in
// internal/tunnel (ProbeHealth, RemoteHealthProbeCommand,
// ClassifyRemoteProbeOutput). Nothing in this package reimplements them, and
// everything except a proven-healthy state fails closed: a supervisor state
// is healthy only when the local daemon, managed ssh child, public remote
// probe, and authenticated expected-instance probe all pass.
package tunnelmgr

import "fmt"

// State is the supervisor's aggregate lifecycle state, persisted in the
// tunnel state file. It deliberately collapses both health dimensions
// (ssh child process, remote probe outcome) into one exhaustive enum so that
// anything the supervisor cannot prove falls into its own non-healthy state
// instead of being folded into a boolean.
type State string

const (
	// StateUnknown means no validated runtime state exists yet. It is the
	// initial state of a fresh record and is never healthy.
	StateUnknown  State = "unknown"
	StateStarting State = "starting"

	// StateHealthy means all four managed-tunnel health checks passed. It is
	// the only healthy state.
	StateHealthy State = "healthy"

	// StateReconnecting means the managed ssh child is not running (spawn
	// failure or exit) and the supervisor is backing off before the next
	// spawn attempt.
	StateReconnecting State = "reconnecting"

	// StateAuthRequired means the ssh child exited because authentication
	// was refused. BatchMode forbids interactive prompts, so this needs a
	// human (ssh-agent, key, passphrase); the supervisor retries slowly.
	StateAuthRequired State = "auth-required"

	// StateHostKeyError means the ssh child exited because the remote host
	// key could not be verified. The supervisor never bypasses host key
	// checking; a human must fix known_hosts.
	StateHostKeyError State = "host-key-error"

	StateLocalDaemonDown             State = "local-daemon-down"
	StateRemoteDown                  State = "remote-down"
	StateRemoteStale                 State = "remote-stale"
	StateRemoteUnverified            State = "remote-unverified"
	StateRemoteUnknown               State = "remote-unknown"
	StateIdentityHelperMissing       State = "identity-helper-missing"
	StateIdentityEndpointUnavailable State = "identity-endpoint-unavailable"
	StateIdentityMismatch            State = "identity-mismatch"
	StateRemoteTokenInvalid          State = "remote-token-invalid"
	StatePortConflict                State = "port-conflict"
	StateConfigError                 State = "config-error"

	// StateCrashLoop means the supervisor hit CrashLoopThreshold consecutive
	// failed starts without reaching the stable window. Automatic restarts
	// stop until the operator fixes the cause and explicitly resets runtime.
	StateCrashLoop State = "crash-loop"

	// StateStopped means the supervisor shut down cleanly (context
	// cancelled, child terminated).
	StateStopped State = "stopped"
)

// Healthy reports whether the state proves the data path works end to end.
// Only StateHealthy qualifies; every other state, including StateUnknown,
// StateRemoteUnknown and StateCrashLoop, fails closed.
func (s State) Healthy() bool {
	return s == StateHealthy
}

// ParseState validates a state string loaded from disk. Anything outside the
// known set — including the empty string — is an error, so a corrupt or
// future-schema state file can never be read as healthy.
func ParseState(s string) (State, error) {
	switch State(s) {
	case StateUnknown, StateStarting, StateHealthy, StateReconnecting,
		StateLocalDaemonDown, StateAuthRequired, StateHostKeyError,
		StateRemoteDown, StateRemoteStale, StateRemoteUnverified,
		StateRemoteUnknown, StateIdentityHelperMissing, StateIdentityEndpointUnavailable,
		StateIdentityMismatch, StateRemoteTokenInvalid,
		StatePortConflict, StateConfigError, StateCrashLoop, StateStopped:
		return State(s), nil
	default:
		return StateUnknown, fmt.Errorf("unknown tunnel supervisor state %q", s)
	}
}
