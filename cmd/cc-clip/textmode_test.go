package main

import (
	"os"
	"testing"

	"github.com/shunmei/cc-clip/internal/daemon"
)

func TestGetTextModeFlagOverridesEnv(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	t.Setenv("CC_CLIP_TEXT", "both")

	os.Args = []string{"cc-clip", "serve", "--text", "off"}
	if got := getTextMode(); got != (daemon.TextMode{}) {
		t.Fatalf("getTextMode() = %v, want off from the flag", got)
	}
	os.Args = []string{"cc-clip", "serve", "--text=read"}
	if got := getTextMode(); got != (daemon.TextMode{Read: true}) {
		t.Fatalf("getTextMode() = %v, want read from --text=read", got)
	}
	os.Args = []string{"cc-clip", "serve"}
	if got := getTextMode(); got != daemon.TextModeBoth {
		t.Fatalf("getTextMode() = %v, want both from env", got)
	}
}

func TestTextModeServeArgs(t *testing.T) {
	if args := textModeServeArgs(daemon.TextModeBoth); args != nil {
		t.Fatalf("default mode must add no serve args (keeps existing plists unchanged), got %q", args)
	}
	args := textModeServeArgs(daemon.TextMode{Write: true})
	if len(args) != 2 || args[0] != "--text" || args[1] != "write" {
		t.Fatalf("textModeServeArgs(write) = %q", args)
	}
}

func TestSetupHostSkipsTextValue(t *testing.T) {
	host, err := hostFromArgs([]string{"--text", "off", "venus"})
	if err != nil || host != "venus" {
		t.Fatalf("hostFromArgs = %q, %v; want venus (the --text value is not a host)", host, err)
	}
}
