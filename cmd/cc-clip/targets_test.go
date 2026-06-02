package main

import (
	"errors"
	"testing"
)

// TestParseDeployTargets exercises the full selector matrix: each single target,
// the --agy/--antigravity alias (including both spellings together as ONE
// target), --all, the no-flag default (explicit=false), the =bool form, and every
// multi-target conflict (errMultipleTargets -> exit 2). The parser must do no IO.
func TestParseDeployTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		want         DeployTargets
		wantExplicit bool
		wantErr      bool
	}{
		{"no target flags -> default (not explicit)", []string{"myhost", "--force", "--port", "1234"}, DeployTargets{}, false, false},
		{"--claude", []string{"myhost", "--claude"}, DeployTargets{Claude: true}, true, false},
		{"--codex", []string{"--codex", "myhost"}, DeployTargets{Codex: true}, true, false},
		{"--opencode", []string{"myhost", "--opencode"}, DeployTargets{Opencode: true}, true, false},
		{"--agy canonical", []string{"myhost", "--agy"}, DeployTargets{Antigravity: true}, true, false},
		{"--antigravity alias", []string{"myhost", "--antigravity"}, DeployTargets{Antigravity: true}, true, false},
		{"--agy --antigravity is one distinct target", []string{"--agy", "--antigravity"}, DeployTargets{Antigravity: true}, true, false},
		{"--all selects everything", []string{"myhost", "--all"}, DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}, true, false},
		{"--claude=true equivalent to --claude", []string{"--claude=true"}, DeployTargets{Claude: true}, true, false},
		{"--claude=false disables -> default", []string{"--claude=false"}, DeployTargets{}, false, false},
		{"--codex --all conflict", []string{"--codex", "--all"}, DeployTargets{}, false, true},
		{"--claude --codex conflict", []string{"--claude", "--codex"}, DeployTargets{}, false, true},
		{"--claude --all conflict", []string{"--claude", "--all"}, DeployTargets{}, false, true},
		{"--opencode --agy conflict", []string{"--opencode", "--agy"}, DeployTargets{}, false, true},
		{"three targets conflict", []string{"--claude", "--codex", "--opencode"}, DeployTargets{}, false, true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, explicit, err := parseDeployTargets(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%+v explicit=%v)", got, explicit)
				}
				if !errors.Is(err, errMultipleTargets) {
					t.Fatalf("error = %v, want errMultipleTargets", err)
				}
				// On conflict the contract is zero targets and explicit=false.
				if got != (DeployTargets{}) || explicit {
					t.Fatalf("on error want zero targets/!explicit, got %+v explicit=%v", got, explicit)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("targets = %+v, want %+v", got, tt.want)
			}
			if explicit != tt.wantExplicit {
				t.Fatalf("explicit = %v, want %v", explicit, tt.wantExplicit)
			}
		})
	}
}

// TestDeployTargetsAny verifies Any() across the zero value, each single target,
// and the full set.
func TestDeployTargetsAny(t *testing.T) {
	t.Parallel()
	if (DeployTargets{}).Any() {
		t.Fatal("zero DeployTargets must report Any()==false")
	}
	for _, s := range []DeployTargets{
		{Claude: true}, {Codex: true}, {Opencode: true}, {Antigravity: true},
	} {
		if !s.Any() {
			t.Fatalf("%+v must report Any()==true", s)
		}
	}
	if !(DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}).Any() {
		t.Fatal("full DeployTargets must report Any()==true")
	}
}
