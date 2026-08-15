package doctor

import (
	"fmt"
	"strings"
	"testing"
)

func TestImageProbeCommandKeepsTokenOutOfCurlArgv(t *testing.T) {
	cmd := imageProbeCommand(18339)
	if strings.Contains(cmd, `-H "Authorization: Bearer ${TOKEN}"`) {
		t.Fatalf("image probe command leaks token through curl argv: %s", cmd)
	}
	if !strings.Contains(cmd, "curl -sf --max-time 5 -K -") {
		t.Fatalf("image probe command must pass curl auth config through stdin: %s", cmd)
	}
	if !strings.Contains(cmd, `printf 'header = "Authorization: Bearer %s"\n'`) {
		t.Fatalf("image probe command must emit Authorization header through curl config: %s", cmd)
	}
}

// TestClassifyTunnelCheck verifies that an SSH transport failure is reported
// as a distinct "could not run over SSH" result rather than being misreported
// as "port not reachable". The latter would mislead the user into debugging a
// forwarding problem when the real issue is the SSH connection itself.
func TestClassifyTunnelCheck(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		err         error
		wantOK      bool
		wantContain string
	}{
		{
			name:        "ssh transport failure",
			out:         "ssh: connect to host example.com port 22: Connection refused",
			err:         fmt.Errorf("exit status 255"),
			wantOK:      false,
			wantContain: "could not run over SSH",
		},
		{
			name:        "daemon answering through tunnel",
			out:         "cc-clip-probe:ok",
			err:         nil,
			wantOK:      true,
			wantContain: "daemon answering",
		},
		{
			name:        "tunnel not reachable",
			out:         "cc-clip-probe:down",
			err:         nil,
			wantOK:      false,
			wantContain: "not reachable from remote",
		},
		{
			// The regression this check exists for: a stale sshd from a
			// previous session still owns the LISTEN socket, so a TCP-only
			// probe called this healthy and doctor stopped looking.
			name:        "stale listener with no daemon behind it",
			out:         "cc-clip-probe:stale",
			err:         nil,
			wantOK:      false,
			wantContain: "no cc-clip daemon answered",
		},
		{
			name:        "remote has no curl to verify with",
			out:         "cc-clip-probe:unverified",
			err:         nil,
			wantOK:      false,
			wantContain: "unverified",
		},
		{
			name:        "probe output not recognized",
			out:         "bash: /dev/tcp: No such file or directory",
			err:         nil,
			wantOK:      false,
			wantContain: "did not complete",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTunnelCheck(tc.out, tc.err, 18339)
			if got.OK != tc.wantOK {
				t.Fatalf("classifyTunnelCheck OK = %v, want %v (msg=%q)", got.OK, tc.wantOK, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantContain) {
				t.Fatalf("classifyTunnelCheck message = %q, want it to contain %q", got.Message, tc.wantContain)
			}
		})
	}
}

// TestClassifyRemoteTokenCheck verifies that an SSH transport failure during
// the remote-token check is reported as a distinct SSH-failure result, not as
// "token file missing" (which would ran-and-found-absence, a different bug).
func TestClassifyRemoteTokenCheck(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		err         error
		wantOK      bool
		wantContain string
	}{
		{
			name:        "ssh transport failure",
			out:         "ssh: Could not resolve hostname badhost",
			err:         fmt.Errorf("exit status 255"),
			wantOK:      false,
			wantContain: "could not run over SSH",
		},
		{
			name:        "token present",
			out:         "present",
			err:         nil,
			wantOK:      true,
			wantContain: "present",
		},
		{
			name:        "token missing",
			out:         "missing",
			err:         nil,
			wantOK:      false,
			wantContain: "token file missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyRemoteTokenCheck(tc.out, tc.err)
			if got.OK != tc.wantOK {
				t.Fatalf("classifyRemoteTokenCheck OK = %v, want %v (msg=%q)", got.OK, tc.wantOK, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantContain) {
				t.Fatalf("classifyRemoteTokenCheck message = %q, want it to contain %q", got.Message, tc.wantContain)
			}
		})
	}
}

// TestRemoteBinProbeCommand pins the remote-bin check's resolution order
// (#111 review, finding 1): the deployed ~/.local/bin/cc-clip is the PRIMARY
// probe — it is the binary cc-clip manages — and the login-shell PATH lookup
// is only the fallback for package-managed hosts. Probing the PATH first
// reported "remote-bin OK" for a stale system copy on hosts whose actually
// deployed binary was missing or truncated. The command must also print the
// resolved path so the operator can see WHICH binary answered.
func TestRemoteBinProbeCommand(t *testing.T) {
	cmd := remoteBinProbeCommand

	localIdx := strings.Index(cmd, `$HOME/.local/bin/cc-clip`)
	loginIdx := strings.Index(cmd, `"$SHELL" -lc`)
	if localIdx == -1 {
		t.Fatalf("probe must check the deployed ~/.local/bin/cc-clip:\n%s", cmd)
	}
	if loginIdx == -1 {
		t.Fatalf("probe must fall back to a login-shell PATH lookup (package-managed hosts):\n%s", cmd)
	}
	if loginIdx < localIdx {
		t.Fatalf("deployed binary must be probed BEFORE the PATH fallback:\n%s", cmd)
	}
	// A plain `command -v` under the session PATH is exactly the resolution
	// the login shell exists to avoid (remotePathPrelude shadows user dirs);
	// the fallback must run inside the login shell only.
	if strings.Contains(strings.ReplaceAll(cmd, `-lc 'command -v cc-clip'`, ""), "command -v cc-clip") {
		t.Fatalf("PATH lookup outside the login shell would resolve under the hardened prelude PATH:\n%s", cmd)
	}
	if !strings.Contains(cmd, `"$bin"`) || !strings.Contains(cmd, `($bin)`) {
		t.Fatalf("probe output must include the resolved path so the operator sees which binary answered:\n%s", cmd)
	}
}
