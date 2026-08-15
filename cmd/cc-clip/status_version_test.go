package main

import (
	"strings"
	"testing"
)

// TestDaemonVersionNotice pins the fail-silent contract: a warning only when a
// mismatch is provable. False "stale daemon" warnings would train operators to
// ignore the real one.
func TestDaemonVersionNotice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		running  string
		own      string
		wantWarn bool
	}{
		{"provable mismatch warns", "0.9.2", "0.9.3", true},
		{"same version silent", "0.9.3", "0.9.3", false},
		{"same after normalization silent", "v0.9.3", "0.9.3", false},
		{"pre-#22 daemon (no version) silent", "", "0.9.3", false},
		{"dev build silent", "0.9.2", "dev", false},
		{"both unknown silent", "", "dev", false},
		{"garbage running version silent", "not-a-version", "0.9.3", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := daemonVersionNotice(tt.running, tt.own)
			if tt.wantWarn {
				if got == "" {
					t.Fatal("expected a warning")
				}
				for _, want := range []string{"WARNING", "cc-clip update"} {
					if !strings.Contains(got, want) {
						t.Fatalf("warning %q must mention %q", got, want)
					}
				}
				return
			}
			if got != "" {
				t.Fatalf("expected silence, got %q", got)
			}
		})
	}
}
