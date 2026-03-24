//go:build !darwin && !windows

package service

import "fmt"

// PlistPath returns an empty string on non-darwin platforms.
func PlistPath() string {
	return ""
}

// Install is not supported on non-darwin platforms.
func Install(binaryPath string, port int) error {
	return fmt.Errorf("launchd service is only supported on macOS")
}

// Uninstall is not supported on non-darwin platforms.
func Uninstall() error {
	return fmt.Errorf("launchd service is only supported on macOS")
}

// Status is not supported on non-darwin platforms.
func Status() (bool, error) {
	return false, fmt.Errorf("launchd service is only supported on macOS")
}
