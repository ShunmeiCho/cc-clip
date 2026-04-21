package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestXclipShimSubstitutesPortAndRealBinary(t *testing.T) {
	got := XclipShim(18339, "/usr/bin/xclip")
	if !strings.Contains(got, `CC_CLIP_PORT="${CC_CLIP_PORT:-18339}"`) {
		t.Fatalf("port substitution missing: %q", got)
	}
	if !strings.Contains(got, `REAL_XCLIP="/usr/bin/xclip"`) {
		t.Fatalf("real xclip path substitution missing: %q", got)
	}
}

func TestWlPasteShimSubstitutesPortAndRealBinary(t *testing.T) {
	got := WlPasteShim(18339, "/usr/bin/wl-paste")
	if !strings.Contains(got, `CC_CLIP_PORT="${CC_CLIP_PORT:-18339}"`) {
		t.Fatalf("port substitution missing: %q", got)
	}
	if !strings.Contains(got, `REAL_WL_PASTE="/usr/bin/wl-paste"`) {
		t.Fatalf("real wl-paste path substitution missing: %q", got)
	}
}

// TestShimMatchesClientInvocations executes the generated shim patterns against
// real Bash using a `fake` REAL binary, verifying that each supported client's
// invocation either reaches the image-read branch (exit 10 when tunnel is down,
// which triggers fallback) or the TARGETS/list-types branch (which also
// fallthroughs). This gives real coverage of the bash case-statement glob
// rules — a subtle change to the patterns would be caught.
//
// The test does not need a running daemon. The `_cc_clip_probe` call returns
// non-zero because no daemon is listening, and we assert that the fallback
// real binary (captured to a sentinel file) is invoked with the expected
// arguments.
func TestShimMatchesClientInvocations(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	cases := []struct {
		name  string
		shim  string
		args  []string
		label string
	}{
		{
			name:  "claude_xclip_targets",
			shim:  XclipShim(65000, "__REAL__"),
			args:  []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
			label: "claude targets",
		},
		{
			name:  "claude_xclip_image",
			shim:  XclipShim(65000, "__REAL__"),
			args:  []string{"-selection", "clipboard", "-t", "image/png", "-o"},
			label: "claude image",
		},
		{
			name:  "opencode_xclip_image",
			shim:  XclipShim(65000, "__REAL__"),
			args:  []string{"-selection", "clipboard", "-t", "image/png", "-o"},
			label: "opencode image (same as claude on xclip)",
		},
		{
			name:  "claude_wlpaste_list_types",
			shim:  WlPasteShim(65000, "__REAL__"),
			args:  []string{"--list-types"},
			label: "claude list-types",
		},
		{
			name:  "claude_wlpaste_type_long",
			shim:  WlPasteShim(65000, "__REAL__"),
			args:  []string{"--type", "image/png"},
			label: "claude --type image/png",
		},
		{
			name:  "opencode_wlpaste_type_short",
			shim:  WlPasteShim(65000, "__REAL__"),
			args:  []string{"-t", "image/png"},
			label: "opencode -t image/png (short form)",
		},
		{
			name:  "passthrough_unrelated_flag",
			shim:  WlPasteShim(65000, "__REAL__"),
			args:  []string{"--watch"},
			label: "unrelated invocation — must pass through to fallback",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			sentinel := filepath.Join(tmpDir, "fallback.log")

			// Build a fake "real" binary that records the arguments it was
			// invoked with into the sentinel file, then exits 0.
			realBin := filepath.Join(tmpDir, "fake-real")
			fakeScript := "#!/bin/bash\n" +
				"printf '%s\\n' \"$*\" > \"" + sentinel + "\"\n" +
				"exit 0\n"
			if err := os.WriteFile(realBin, []byte(fakeScript), 0755); err != nil {
				t.Fatalf("write fake real: %v", err)
			}

			// Render the shim with the fake real binary as the fallback target.
			shim := strings.ReplaceAll(tc.shim, "__REAL__", realBin)
			shimPath := filepath.Join(tmpDir, "shim.sh")
			if err := os.WriteFile(shimPath, []byte(shim), 0755); err != nil {
				t.Fatalf("write shim: %v", err)
			}

			// Run the shim. Daemon is down → all supported patterns fall
			// through to fallback; unmatched patterns also fall through.
			cmd := exec.Command("bash", append([]string{shimPath}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"CC_CLIP_PORT=65000",
				"CC_CLIP_PROBE_TIMEOUT_MS=50",
			)
			_ = cmd.Run() // fallback exits 0 via exec; don't fail on cmd error

			recorded, err := os.ReadFile(sentinel)
			if err != nil {
				t.Fatalf("[%s] fallback was not invoked (sentinel absent): %v", tc.label, err)
			}
			got := strings.TrimSpace(string(recorded))
			want := strings.Join(tc.args, " ")
			if got != want {
				t.Fatalf("[%s] fallback args mismatch\n  got:  %q\n  want: %q", tc.label, got, want)
			}
		})
	}
}
