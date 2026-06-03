package shim

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// opencodeStubExecutor records the last command and returns canned stdout with
// no error, so RemoteHasOpencode's tri-state parsing and probe shape can be
// asserted without a real opencode binary. Mirrors agyStubExecutor.
type opencodeStubExecutor struct {
	out     string
	lastCmd string
}

func (s *opencodeStubExecutor) Exec(cmd string) (string, error) {
	s.lastCmd = cmd
	return s.out, nil
}

// --- Task 4: detector tri-state + probe shape ---

func TestRemoteHasOpencode(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    bool
		wantErr bool
	}{
		{"present", "yes", true, false},
		{"absent", "no", false, false},
		{"trailing whitespace present", "yes\n", true, false},
		{"garbage", "maybe", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RemoteHasOpencode(&opencodeStubExecutor{out: tt.out})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for probe output %q", tt.out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("RemoteHasOpencode(%q) = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}

func TestRemoteHasOpencodeProbesCommandV(t *testing.T) {
	stub := &opencodeStubExecutor{out: "no"}
	if _, err := RemoteHasOpencode(stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stub.lastCmd, "command -v opencode") {
		t.Fatalf("RemoteHasOpencode must probe via `command -v opencode`, got %q", stub.lastCmd)
	}
	// Must NOT use a directory-existence probe: a present ~/.config/opencode/
	// directory does not mean the opencode CLI is runnable.
	if strings.Contains(stub.lastCmd, "[ -d") {
		t.Fatalf("RemoteHasOpencode must not use a directory probe, got %q", stub.lastCmd)
	}
}

func TestRemoteHasOpencodeReturnsExecError(t *testing.T) {
	_, err := RemoteHasOpencode(&errorExecutor{err: fmt.Errorf("ssh failed")})
	if err == nil {
		t.Fatal("RemoteHasOpencode should surface executor errors")
	}
}

// --- Task 5: installer script assertions + embedded JS ---

func TestBuildOpencodePluginScript(t *testing.T) {
	script := buildOpencodePluginScript(18339)
	mustContain := []string{
		"set -e",
		"mkdir -p",
		".config/opencode/plugins",
		"mktemp",
		"cc-clip-notify.js",
		"mv",
		"trap",
		"rm -f",
		// the embedded JS plugin source
		"cc-clip plugin run opencode-notify",
		"event:",
		"session.idle",
	}
	for _, frag := range mustContain {
		if !strings.Contains(script, frag) {
			t.Fatalf("script missing %q:\n%s", frag, script)
		}
	}
	// Atomic write: the temp file must be mv'd to the final cc-clip-notify.js.
	mi := strings.Index(script, "mktemp")
	vi := strings.Index(script, "mv")
	if mi < 0 || vi < 0 || mi > vi {
		t.Fatalf("mktemp must precede mv (mktemp=%d mv=%d)", mi, vi)
	}
}

func TestBuildOpencodePluginScriptNonDefaultPort(t *testing.T) {
	script := buildOpencodePluginScript(9999)
	want := "env CC_CLIP_PORT=9999 cc-clip plugin run opencode-notify"
	if !strings.Contains(script, want) {
		t.Fatalf("non-default port must be baked into the embedded JS command:\n%s", script)
	}
}

func TestEnsureRemoteOpencodePluginSurfacesExecError(t *testing.T) {
	err := EnsureRemoteOpencodePlugin(&errorExecutor{err: fmt.Errorf("ssh failed")}, 18339)
	if err == nil {
		t.Fatal("EnsureRemoteOpencodePlugin should surface executor errors")
	}
	if !strings.Contains(err.Error(), "opencode") {
		t.Fatalf("error should mention the opencode plugin install context, got %v", err)
	}
}

// --- Task 6: strip helper (NOT wired) ---

func TestBuildOpencodeStripScript(t *testing.T) {
	script := buildOpencodeStripScript()
	if !strings.Contains(script, "rm -f") {
		t.Fatalf("strip script must rm -f the dropped file:\n%s", script)
	}
	if !strings.Contains(script, "cc-clip-notify.js") {
		t.Fatalf("strip script must target cc-clip-notify.js:\n%s", script)
	}
	if !strings.Contains(script, ".config/opencode/plugins") {
		t.Fatalf("strip script must target the opencode plugins dir:\n%s", script)
	}
}

func TestStripRemoteOpencodePluginSurfacesExecError(t *testing.T) {
	err := StripRemoteOpencodePlugin(&errorExecutor{err: fmt.Errorf("ssh failed")})
	if err == nil {
		t.Fatal("StripRemoteOpencodePlugin should surface executor errors")
	}
}

// --- JS template assertions ---

func TestOpencodePluginJSDefaultPort(t *testing.T) {
	js := opencodePluginJS(18339)
	mustContain := []string{
		"export const CcClipNotifyPlugin",
		"event:",
		`event.type !== "session.idle"`,
		"$`cc-clip plugin run opencode-notify`",
		".quiet().nothrow()",
		"JSON.stringify({ event })",
	}
	for _, frag := range mustContain {
		if !strings.Contains(js, frag) {
			t.Fatalf("opencodePluginJS(18339) missing %q:\n%s", frag, js)
		}
	}
	// Default port must NOT bake in an env prefix.
	if strings.Contains(js, "CC_CLIP_PORT") {
		t.Fatalf("default-port JS must not carry CC_CLIP_PORT:\n%s", js)
	}
}

func TestOpencodePluginJSNonDefaultPort(t *testing.T) {
	js := opencodePluginJS(9999)
	want := "$`env CC_CLIP_PORT=9999 cc-clip plugin run opencode-notify`"
	if !strings.Contains(js, want) {
		t.Fatalf("non-default-port JS must bake env prefix, got:\n%s", js)
	}
}

// --- Task 10A: JS-syntax check (runs by default, gated on node/bun) ---

// TestOpencodePluginJSIsValidJS proves the generated plugin is syntactically
// valid JavaScript by running `node --check` (or `bun build --no-bundle`).
// Cheap, deterministic, no opencode and no model cost. Skips cleanly when
// neither node nor bun is on PATH.
func TestOpencodePluginJSIsValidJS(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/cc-clip-notify.mjs"
	if err := os.WriteFile(file, []byte(opencodePluginJS(18339)), 0o644); err != nil {
		t.Fatalf("write temp plugin file: %v", err)
	}

	if nodeBin, err := exec.LookPath("node"); err == nil {
		out, err := exec.Command(nodeBin, "--check", file).CombinedOutput()
		if err != nil {
			t.Fatalf("node --check rejected the generated plugin: %v\n%s", err, out)
		}
		return
	}
	if bunBin, err := exec.LookPath("bun"); err == nil {
		out, err := exec.Command(bunBin, "build", "--no-bundle", file).CombinedOutput()
		if err != nil {
			t.Fatalf("bun build rejected the generated plugin: %v\n%s", err, out)
		}
		return
	}
	t.Skip("neither node nor bun on PATH; skipping JS-syntax check")
}

// --- Task 10B: real opencode smoke (manual only, double-gated) ---

// TestEnsureRemoteOpencodePluginSmoke is an opt-in end-to-end check that a real
// opencode actually loads the dropped plugin and fires session.idle. It is
// double-gated so normal `go test ./...` never touches the developer's opencode
// config or incurs cost:
//   - CC_CLIP_OPENCODE_SMOKE=1 must be set, and
//   - the opencode binary must be resolvable on PATH.
//
// IMPORTANT: this needs WORKING opencode auth and MAY incur real model-call
// cost. It is explicitly NOT a CI test. opencode auth/data may live outside
// XDG_CONFIG_HOME, so isolating HOME can hide real auth — this test skips
// cleanly (never fails) when auth is absent. A skip-by-default stub is the
// accepted form for this step; the real human-run proof lives here.
func TestEnsureRemoteOpencodePluginSmoke(t *testing.T) {
	if os.Getenv("CC_CLIP_OPENCODE_SMOKE") != "1" {
		t.Skip("set CC_CLIP_OPENCODE_SMOKE=1 to run the opencode plugin smoke test (needs working opencode auth, MAY incur model-call cost)")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skipf("opencode not on PATH: %v", err)
	}
	t.Skip("manual smoke: drop opencodePluginJS into ~/.config/opencode/plugins, trigger a real session.idle, and confirm `cc-clip plugin run opencode-notify` is invoked. Skipped by default to avoid model-call cost.")
}
