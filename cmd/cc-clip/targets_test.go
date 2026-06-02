package main

import (
	"bytes"
	"errors"
	"strings"
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

// TestMenuSelection maps each valid menu choice (surrounding whitespace
// tolerated) and rejects out-of-range / non-numeric input.
func TestMenuSelection(t *testing.T) {
	t.Parallel()
	valid := map[string]DeployTargets{
		"1":   {Claude: true},
		"2":   {Codex: true},
		"3":   {Opencode: true},
		"4":   {Antigravity: true},
		"5":   {Claude: true, Codex: true, Opencode: true, Antigravity: true},
		" 3 ": {Opencode: true},
	}
	for in, want := range valid {
		if got, ok := menuSelection(in); !ok || got != want {
			t.Errorf("menuSelection(%q) = %+v,%v; want %+v,true", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "0", "6", "x", "12", "-1"} {
		if got, ok := menuSelection(bad); ok {
			t.Errorf("menuSelection(%q) = %+v,true; want ok=false", bad, got)
		}
	}
}

// TestPromptDeployTargets covers a valid selection, re-prompt after invalid
// input, EOF, and exhausted attempts.
func TestPromptDeployTargets(t *testing.T) {
	t.Parallel()

	t.Run("valid selection returns targets and renders menu", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		got, ok := promptDeployTargets(strings.NewReader("2\n"), &out)
		if !ok || got != (DeployTargets{Codex: true}) {
			t.Fatalf("got %+v,%v; want {Codex:true},true", got, ok)
		}
		if !strings.Contains(out.String(), "Select deployment target:") {
			t.Fatalf("menu not rendered:\n%s", out.String())
		}
	})

	t.Run("re-prompts after invalid then accepts valid", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		got, ok := promptDeployTargets(strings.NewReader("9\n3\n"), &out)
		if !ok || got != (DeployTargets{Opencode: true}) {
			t.Fatalf("got %+v,%v; want {Opencode:true},true", got, ok)
		}
		if !strings.Contains(out.String(), "invalid selection") {
			t.Fatalf("expected invalid-selection notice:\n%s", out.String())
		}
	})

	t.Run("EOF returns ok=false", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		if got, ok := promptDeployTargets(strings.NewReader(""), &out); ok {
			t.Fatalf("EOF must yield ok=false, got %+v", got)
		}
	})

	t.Run("exhausted attempts returns ok=false", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		if got, ok := promptDeployTargets(strings.NewReader("9\n8\n7\n6\n"), &out); ok {
			t.Fatalf("3 invalid attempts must yield ok=false, got %+v", got)
		}
	})
}

// TestResolveImplicitTargets covers the non-TTY fallback (with stderr warning),
// the TTY menu path, and a TTY user declining (EOF -> fallback). The fallback is
// always the minimal-side-effect {Claude} so an unattended run never silently
// triggers high-side-effect installs.
func TestResolveImplicitTargets(t *testing.T) {
	t.Parallel()
	fallback := DeployTargets{Claude: true}

	t.Run("non-TTY falls back and warns on stderr", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		got := resolveImplicitTargets(false, strings.NewReader(""), &out, &errOut, fallback, "claude")
		if got != fallback {
			t.Fatalf("got %+v, want fallback %+v", got, fallback)
		}
		if !strings.Contains(errOut.String(), "not a TTY") || !strings.Contains(errOut.String(), "claude") {
			t.Fatalf("expected non-TTY warning naming the fallback:\n%s", errOut.String())
		}
		if out.Len() != 0 {
			t.Fatalf("non-TTY path must not render the menu to out:\n%s", out.String())
		}
	})

	t.Run("TTY presents menu and returns selection", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		got := resolveImplicitTargets(true, strings.NewReader("5\n"), &out, &errOut, fallback, "claude")
		if got != (DeployTargets{Claude: true, Codex: true, Opencode: true, Antigravity: true}) {
			t.Fatalf("got %+v, want all", got)
		}
		if !strings.Contains(out.String(), "Select deployment target:") {
			t.Fatalf("menu not rendered:\n%s", out.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("TTY path must not warn on stderr:\n%s", errOut.String())
		}
	})

	t.Run("TTY declined (EOF) falls back to default", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		got := resolveImplicitTargets(true, strings.NewReader(""), &out, &errOut, fallback, "claude")
		if got != fallback {
			t.Fatalf("got %+v, want fallback %+v", got, fallback)
		}
		if !strings.Contains(out.String(), "defaulting to claude") {
			t.Fatalf("expected default notice:\n%s", out.String())
		}
	})
}

// TestHostFromArgs verifies flag-tolerant host extraction so the host may appear
// before OR after flags (replacing the positional os.Args[2] assumption). The
// args slice is os.Args[2:] (everything after the subcommand). Space-form value
// flags (--port, --local-bin) consume the following token, which must NOT be
// mistaken for the host; their =value form keeps the value inline.
func TestHostFromArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{"host then flags", []string{"myhost", "--codex", "--port", "1234"}, "myhost", false},
		{"flag then host", []string{"--codex", "myhost"}, "myhost", false},
		{"bool =value flag then host", []string{"--force=true", "myhost"}, "myhost", false},
		{"flags surrounding host", []string{"--codex", "myhost", "--force"}, "myhost", false},
		{"port space-value skipped, host after", []string{"--port", "9", "myhost"}, "myhost", false},
		{"port =value inline, host after", []string{"--port=9", "myhost"}, "myhost", false},
		{"local-bin space-value skipped, host after", []string{"--local-bin", "/tmp/cc-clip", "myhost"}, "myhost", false},
		{"local-bin =value inline, host after", []string{"--local-bin=/tmp/cc-clip", "myhost"}, "myhost", false},
		{"no host only flags", []string{"--codex", "--force"}, "", true},
		{"dangling value flag is not a host", []string{"--port", "1234"}, "", true},
		{"empty", []string{}, "", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := hostFromArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got host=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Fatalf("host = %q, want %q", got, tt.want)
			}
		})
	}
}
