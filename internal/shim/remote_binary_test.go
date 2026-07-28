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
