//go:build !windows

package main

import "log"

func cmdHotkey() {
	log.Fatal("hotkey is only supported on Windows")
}
