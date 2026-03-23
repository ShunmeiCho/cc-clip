//go:build windows

package main

import "os"

// shutdownSignals returns the signals to listen for graceful shutdown.
// On Windows, only os.Interrupt (Ctrl+C) is supported.
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
