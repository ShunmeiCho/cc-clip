package tunnelmgr

import (
	"context"
	"errors"
	"time"
)

// Authentication results describe only SSH authentication, never tunnel health.
// Unknown and timeout remain distinct from proven authentication failures.
type AuthResult struct {
	State string `json:"state"`
}

func (r AuthResult) Valid() bool {
	switch r.State {
	case "ok", "auth-required", "host-key-error", "connection-error", "timeout":
		return true
	default:
		return false
	}
}

func CheckAuthentication(ctx context.Context, spec Spec) AuthResult {
	return checkAuthentication(ctx, &Backend{Spec: spec})
}

func checkAuthentication(ctx context.Context, backend *Backend) AuthResult {
	if ctx.Err() != nil {
		return AuthResult{State: "timeout"}
	}
	// Reuse the forwarding-free master and its owned-child cleanup. No reverse
	// listener is requested and no remote clipboard/hook configuration changes.
	if err := backend.Start(); err != nil {
		return AuthResult{State: "connection-error"}
	}
	defer backend.Stop(defaultStopGrace)
	err := backend.WaitReady(ctx, 15*time.Second)
	if err == nil {
		return AuthResult{State: "ok"}
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return AuthResult{State: "timeout"}
	}
	switch classifyExit(backend.LastExit()) {
	case StateAuthRequired:
		return AuthResult{State: "auth-required"}
	case StateHostKeyError:
		return AuthResult{State: "host-key-error"}
	default:
		return AuthResult{State: "connection-error"}
	}
}
