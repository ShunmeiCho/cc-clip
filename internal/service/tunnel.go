package service

import (
	"context"

	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

type TunnelJob struct {
	Loaded   bool
	Process  string // running, stopped, or unknown
	PID      int
	LastExit *int
}

// TunnelManager manages per-host supervisor jobs independently of the clipboard
// daemon. The service-management CLI uses this interface for lifecycle and
// service-context authentication checks; constructing a manager installs nothing.
type TunnelManager interface {
	// Install requires an enabled, validated ServiceConfig. Callers must do
	// service-context authentication preflight before exposing this operation
	// through an enable command. It never edits SSH config or installs a daemon.
	Install(binaryPath, id string) error
	// Uninstall removes the job/plist only; callers own desired-state changes.
	Uninstall(id string) error
	// Status reports job registration, not process liveness or tunnel health.
	Status(id string) (bool, error)
	Inspect(id string) (TunnelJob, error)
	Stop(id string) error
	Preflight(ctx context.Context, binaryPath string, spec tunnelmgr.Spec) (tunnelmgr.AuthResult, error)
	LogPath(id string) (string, error)
}
