package main

import (
	"errors"
	"testing"
)

// TestDecideN0SkipOnDetectError pins the fail-closed policy: the N0 v0.7.0 gate
// may be skipped after a detection error ONLY when the remote is provably
// fresh (no claude install) AND the presence probe itself succeeded.
func TestDecideN0SkipOnDetectError(t *testing.T) {
	probeBoom := errors.New("probe failed")
	cases := []struct {
		name     string
		exists   bool
		probeErr error
		want     bool
	}{
		{"fresh_no_install_skips", false, nil, true},
		{"install_present_fails_closed", true, nil, false},
		{"probe_error_absent_fails_closed", false, probeBoom, false},
		{"probe_error_present_fails_closed", true, probeBoom, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideN0SkipOnDetectError(tc.exists, tc.probeErr); got != tc.want {
				t.Errorf("decideN0SkipOnDetectError(%v, %v) = %v, want %v", tc.exists, tc.probeErr, got, tc.want)
			}
		})
	}
}
