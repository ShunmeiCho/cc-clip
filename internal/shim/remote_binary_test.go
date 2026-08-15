package shim

import (
	"errors"
	"strings"
	"testing"
)

type remoteBinaryExecutor struct {
	output  string
	err     error
	command string
}

func (e *remoteBinaryExecutor) Exec(command string) (string, error) {
	e.command = command
	return e.output, e.err
}

func TestInspectRemoteBinary(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	executor := &remoteBinaryExecutor{
		output: "/nix/store/pkg's/bin/cc-clip\ncc-clip v0.9.1\n" + hash,
	}

	info, err := InspectRemoteBinary(executor)
	if err != nil {
		t.Fatalf("InspectRemoteBinary returned error: %v", err)
	}
	if got, want := info.Path, "/nix/store/pkg's/bin/cc-clip"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got, want := info.Version, "v0.9.1"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if info.Hash != hash {
		t.Errorf("Hash = %q, want %q", info.Hash, hash)
	}
	if got, want := info.Command(), `'/nix/store/pkg'\''s/bin/cc-clip'`; got != want {
		t.Errorf("Command() = %q, want %q", got, want)
	}
	for _, fragment := range []string{
		"command -v cc-clip",
		"sha256sum",
		"shasum -a 256",
	} {
		if !strings.Contains(executor.command, fragment) {
			t.Errorf("inspection command missing %q: %s", fragment, executor.command)
		}
	}
}

func TestInspectRemoteBinaryErrors(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name    string
		output  string
		err     error
		wantErr string
	}{
		{
			name:    "remote command failed",
			err:     errors.New("exit status 127"),
			wantErr: "remote cc-clip",
		},
		{
			name:    "malformed output",
			output:  "/usr/bin/cc-clip\ncc-clip v0.9.1",
			wantErr: "unexpected inspection output",
		},
		{
			name:    "invalid version",
			output:  "/usr/bin/cc-clip\nv0.9.1\n" + hash,
			wantErr: "unexpected version output",
		},
		{
			name:    "invalid hash",
			output:  "/usr/bin/cc-clip\ncc-clip v0.9.1\nsha256:not-hex",
			wantErr: "invalid remote binary hash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &remoteBinaryExecutor{output: tt.output, err: tt.err}
			info, err := InspectRemoteBinary(executor)
			if err == nil {
				t.Fatalf("InspectRemoteBinary() = %+v, want error", info)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestInspectRemoteBinaryResolvesViaLoginShell pins the fix for the PATH
// shadowing defect: WrapRemoteShell prepends remotePathPrelude to every remote
// command, putting system bin directories ahead of the inherited $PATH, so a
// plain `command -v cc-clip` can never see ~/.nix-profile/bin, ~/.local/bin,
// pipx or asdf — the package managers issue #110 was filed about. Resolution
// must therefore happen under the user's own login shell, with the plain
// lookup only as a fallback.
func TestInspectRemoteBinaryResolvesViaLoginShell(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	executor := &remoteBinaryExecutor{
		output: "/home/u/.nix-profile/bin/cc-clip\ncc-clip v0.9.1\n" + hash,
	}

	if _, err := InspectRemoteBinary(executor); err != nil {
		t.Fatalf("InspectRemoteBinary returned error: %v", err)
	}

	cmd := executor.command
	// The login-shell resolution must come BEFORE the plain fallback, and the
	// candidate it yields must be validated executable so rc-file noise on the
	// last output line cannot be adopted as a path.
	loginIdx := strings.Index(cmd, `"$SHELL" -lc`)
	plainIdx := strings.LastIndex(cmd, "command -v cc-clip")
	if loginIdx == -1 {
		t.Fatalf("inspection command must resolve via the login shell:\n%s", cmd)
	}
	if plainIdx == -1 || plainIdx < loginIdx {
		t.Fatalf("plain command -v must remain as the fallback AFTER the login-shell attempt:\n%s", cmd)
	}
	if !strings.Contains(cmd, `-x "$candidate"`) {
		t.Fatalf("login-shell candidate must be validated executable before adoption:\n%s", cmd)
	}
}

// TestInspectRemoteBinaryErrorsAreActionablePerExitCode verifies the three
// distinct remote failure modes are not collapsed into one message.
func TestInspectRemoteBinaryErrorsAreActionablePerExitCode(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantAll []string
	}{
		{"not found (127)", fakeExitError{code: 127}, []string{"no cc-clip executable found", "--use-remote-bin"}},
		{"not executable (126)", fakeExitError{code: 126}, []string{"not executable"}},
		{"no hash tool (125)", fakeExitError{code: 125}, []string{"sha256sum", "shasum"}},
	}
	seen := map[string]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &remoteBinaryExecutor{err: tt.err}
			_, err := InspectRemoteBinary(executor)
			if err == nil {
				t.Fatal("want error")
			}
			for _, want := range tt.wantAll {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q must mention %q", err, want)
				}
			}
			if prev, dup := seen[err.Error()]; dup {
				t.Fatalf("cases %q and %q share the message %q; failure modes must stay distinguishable", prev, tt.name, err)
			}
			seen[err.Error()] = tt.name
		})
	}
}

// fakeExitError mimics an *exec.ExitError-style exit code carrier without
// spawning a process. InspectRemoteBinary must read the code via the
// exitCoder interface, not by type-asserting exec.ExitError, so SSH transport
// wrappers and tests can both convey remote exit codes.
type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return "exit status" }
func (e fakeExitError) ExitCode() int { return e.code }
