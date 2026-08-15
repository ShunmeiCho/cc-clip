package main

import (
	"errors"
	"strings"
	"testing"
)

// staticProbe returns a focusProbe that yields the given results in order,
// repeating the last one once exhausted.
func staticProbe(results ...focusResult) focusProbe {
	i := 0
	return func() focusResult {
		r := results[i]
		if i < len(results)-1 {
			i++
		}
		return r
	}
}

func TestFocusGuardAllowsPasteWhenFocusIsUnchanged(t *testing.T) {
	probe := staticProbe(focusResult{Handle: 0x1234, Known: true})

	guard, err := newFocusGuard(probe)
	if err != nil {
		t.Fatalf("capturing a known foreground window must succeed: %v", err)
	}
	if err := guard.verify(); err != nil {
		t.Fatalf("unchanged focus must verify: %v", err)
	}
}

// TestFocusGuardAbortsWhenFocusMoved is the core of issue #43: the clipboard
// holds the remote path across the delay plus a PowerShell cold start, and if
// the user switches to a password manager or a browser URL bar in that window,
// the keystroke lands in the wrong trust boundary.
func TestFocusGuardAbortsWhenFocusMoved(t *testing.T) {
	probe := staticProbe(
		focusResult{Handle: 0x1234, Known: true}, // capture
		focusResult{Handle: 0x5678, Known: true}, // verify: different window
	)

	guard, err := newFocusGuard(probe)
	if err != nil {
		t.Fatalf("capture must succeed: %v", err)
	}

	err = guard.verify()
	if err == nil {
		t.Fatal("a focus change must abort the paste")
	}
	if !errors.Is(err, errFocusChanged) {
		t.Fatalf("expected errFocusChanged, got %v", err)
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("abort error must say the paste was aborted, got %q", err)
	}
}

// TestFocusGuardFailsClosedWhenFocusIsUnknown pins the tri-state contract.
// GetForegroundWindow returns NULL while a window is losing activation, so
// "unknown" is a real third state. Collapsing it into "same" would fail open
// and paste into whatever happens to be focused — the exact defect class this
// guard exists to prevent.
func TestFocusGuardFailsClosedWhenFocusIsUnknown(t *testing.T) {
	t.Run("unknown at capture", func(t *testing.T) {
		probe := staticProbe(focusResult{Known: false})

		_, err := newFocusGuard(probe)
		if err == nil {
			t.Fatal("an undeterminable foreground window must not produce a usable guard")
		}
		if !errors.Is(err, errFocusUnknown) {
			t.Fatalf("expected errFocusUnknown, got %v", err)
		}
	})

	t.Run("unknown at verify", func(t *testing.T) {
		probe := staticProbe(
			focusResult{Handle: 0x1234, Known: true},
			focusResult{Known: false},
		)

		guard, err := newFocusGuard(probe)
		if err != nil {
			t.Fatalf("capture must succeed: %v", err)
		}

		err = guard.verify()
		if err == nil {
			t.Fatal("an undeterminable foreground window at verify time must abort")
		}
		if !errors.Is(err, errFocusUnknown) {
			t.Fatalf("expected errFocusUnknown, got %v", err)
		}
	})
}

// TestFocusGuardTreatsZeroHandleAsUnknown guards against a probe that reports
// Known while handing back a NULL handle. NULL identifies no window, so it can
// never be compared meaningfully.
func TestFocusGuardTreatsZeroHandleAsUnknown(t *testing.T) {
	probe := staticProbe(focusResult{Handle: 0, Known: true})

	if _, err := newFocusGuard(probe); !errors.Is(err, errFocusUnknown) {
		t.Fatalf("a NULL handle must be treated as unknown, got %v", err)
	}
}

func TestFocusGuardErrorsExplainTheRemedy(t *testing.T) {
	changed := staticProbe(
		focusResult{Handle: 0x1234, Known: true},
		focusResult{Handle: 0x5678, Known: true},
	)
	guard, err := newFocusGuard(changed)
	if err != nil {
		t.Fatalf("capture must succeed: %v", err)
	}
	msg := guard.verify().Error()

	// The operator needs to know nothing was typed into the other window and
	// what to do next.
	for _, want := range []string{"focus", "aborted", "retry"} {
		if !strings.Contains(strings.ToLower(msg), want) {
			t.Fatalf("focus-change error must mention %q, got %q", want, msg)
		}
	}
}
