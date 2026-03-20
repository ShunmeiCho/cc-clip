//go:build !windows

package main

import (
	"fmt"
	"time"
)

func defaultRemoteHost() (string, bool, error) {
	return "", false, nil
}

func pasteRemotePath(remotePath, imagePath string, delay time.Duration, restoreClipboard bool) error {
	return fmt.Errorf("--paste is only supported on Windows")
}
