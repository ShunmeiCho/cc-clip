//go:build !windows

package main

import (
	"os"
	"syscall"
)

// shutdownSignals returns the signals to listen for graceful shutdown.
// On Unix, both SIGINT and SIGTERM are caught (kill <pid> sends SIGTERM).
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
