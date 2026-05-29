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
			name:        "tunnel reachable",
			out:         "tunnel ok",
			err:         nil,
			wantOK:      true,
			wantContain: "forwarded",
		},
		{
			name:        "tunnel not reachable",
			out:         "tunnel fail",
			err:         nil,
			wantOK:      false,
			wantContain: "not reachable from remote",
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
