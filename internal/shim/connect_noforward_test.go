package shim

import (
	"strings"
	"testing"
)

// TestNoForwardPrefixContainsClearAllForwardings pins the package-level prefix
// used by the legacy host helpers (DetectRemoteArch, UploadBinary, RemoteExec,
// WriteRemoteToken). Without ClearAllForwardings=yes these one-shot ssh/scp
// invocations would inherit the operator's global RemoteForward, competing with
// the active tunnel (see "Known Pitfalls": ControlMaster + RemoteForward).
func TestNoForwardPrefixContainsClearAllForwardings(t *testing.T) {
	joined := strings.Join(noForwardPrefix, " ")
	if joined != "-o ClearAllForwardings=yes" {
		t.Fatalf("noForwardPrefix = %q, want %q", joined, "-o ClearAllForwardings=yes")
	}
}

// TestNoForwardPrefixGuardsBeforeOptionSeparator verifies the guard option is
// emitted before the "--" separator (so ssh parses it as an option, not as the
// host argument) when threaded through sshHostArgs.
func TestNoForwardPrefixGuardsBeforeOptionSeparator(t *testing.T) {
	args := sshHostArgs(noForwardPrefix, "host", "uname -sm")
	sepIdx := indexOf(args, "--")
	if sepIdx < 0 {
		t.Fatalf("sshHostArgs output missing %q separator: %v", "--", args)
	}
	clearIdx := indexOf(args, "ClearAllForwardings=yes")
	if clearIdx < 0 {
		t.Fatalf("sshHostArgs output missing ClearAllForwardings guard: %v", args)
	}
	if clearIdx > sepIdx {
		t.Fatalf("ClearAllForwardings (idx %d) must come before %q separator (idx %d): %v", clearIdx, "--", sepIdx, args)
	}
}

// TestLegacyHostHelperArgsGuardForwardings asserts each of the four legacy
// host helpers constructs its ssh/scp argument vector with the no-forward
// guard. These are arg-level assertions only — no real SSH is opened.
func TestLegacyHostHelperArgsGuardForwardings(t *testing.T) {
	const host = "example.host"

	tests := []struct {
		name string
		args []string
	}{
		{"detectArch", detectRemoteArchArgs(host)},
		{"uploadBinaryScp", uploadBinaryScpArgs(host, "/tmp/local", "/tmp/remote")},
		{"uploadBinaryChmod", uploadBinaryChmodArgs(host, "/tmp/remote")},
		{"remoteExec", remoteExecArgs(host, "echo hi")},
		{"writeRemoteToken", writeRemoteTokenArgs(host)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearIdx := indexOf(tt.args, "ClearAllForwardings=yes")
			if clearIdx < 0 {
				t.Fatalf("%s args missing ClearAllForwardings guard: %v", tt.name, tt.args)
			}
			sepIdx := indexOf(tt.args, "--")
			if sepIdx < 0 {
				t.Fatalf("%s args missing %q separator: %v", tt.name, "--", tt.args)
			}
			if clearIdx > sepIdx {
				t.Fatalf("%s: guard (idx %d) must precede %q separator (idx %d): %v", tt.name, clearIdx, "--", sepIdx, tt.args)
			}
		})
	}
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
