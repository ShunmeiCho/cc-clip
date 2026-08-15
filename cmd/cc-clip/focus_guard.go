package main

import (
	"errors"
	"fmt"
)

// focusResult is one observation of which window currently has focus.
//
// Known is deliberately separate from Handle rather than being inferred from
// it. GetForegroundWindow returns NULL while a window is losing activation, so
// "I could not tell" is a genuine third state alongside "same window" and
// "different window". Folding it into either of the other two turns the guard
// into a boolean that fails open exactly when focus is in motion — which is
// the only moment the guard matters.
type focusResult struct {
	Handle uintptr
	Known  bool
}

// focusProbe reports which window currently has focus. The production
// implementation lives with its only caller in send_windows.go; tests inject
// their own so this decision logic stays runnable on every platform.
type focusProbe func() focusResult

var (
	// errFocusUnknown means the focused window could not be determined,
	// either when capturing or when re-checking.
	errFocusUnknown = errors.New("focus could not be determined (no foreground window)")

	// errFocusChanged means focus moved between the clipboard write and the
	// keystroke.
	errFocusChanged = errors.New("focus changed during paste")
)

// focusGuard remembers the window a paste was aimed at so the keystroke can be
// withheld if focus moved in the meantime.
//
// The exposure window is wider than the configured delay: windowsSendCtrlShiftV
// spawns a fresh `powershell -STA -NoProfile` process, so the clipboard holds
// the remote path for the delay plus that cold start. Anything the user focuses
// during that period — a password manager, a chat input, a browser URL bar —
// would otherwise receive the keystroke.
type focusGuard struct {
	probe  focusProbe
	origin uintptr
}

// newFocusGuard captures the window that currently has focus. It fails when the
// foreground window cannot be identified, so callers never proceed on an
// unverifiable target.
func newFocusGuard(probe focusProbe) (*focusGuard, error) {
	got := probe()
	if !got.Known || got.Handle == 0 {
		return nil, fmt.Errorf("%w; paste aborted before the clipboard was touched", errFocusUnknown)
	}
	return &focusGuard{probe: probe, origin: got.Handle}, nil
}

// verify reports whether focus still rests on the captured window. A non-nil
// error means the keystroke must not be sent.
func (g *focusGuard) verify() error {
	got := g.probe()
	if !got.Known || got.Handle == 0 {
		return fmt.Errorf("%w; paste aborted, nothing was typed — retry with the target window focused", errFocusUnknown)
	}
	if got.Handle != g.origin {
		return fmt.Errorf(
			"%w; paste aborted, nothing was typed into the other window — retry without switching windows",
			errFocusChanged,
		)
	}
	return nil
}
