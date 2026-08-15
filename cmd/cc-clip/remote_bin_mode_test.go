package main

import (
	"testing"

	"github.com/shunmei/cc-clip/internal/shim"
)

func TestShouldAdoptRemoteBinMode(t *testing.T) {
	t.Parallel()
	recorded := &shim.DeployState{UseRemoteBin: true}
	uploaded := &shim.DeployState{}

	tests := []struct {
		name     string
		flag     bool
		localBin string
		state    *shim.DeployState
		want     bool
	}{
		// The case the stickiness exists for: `cc-clip update` tells the user
		// to run `connect <host> --force` with no mode flag at all.
		{"recorded host, no flags -> adopt", false, "", recorded, true},
		{"flag given -> caller already handles it", true, "", recorded, false},
		{"--local-bin is the explicit way back to uploads", false, "/tmp/cc-clip", recorded, false},
		{"uploaded-mode state -> no adoption", false, "", uploaded, false},
		{"no prior state -> no adoption", false, "", nil, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldAdoptRemoteBinMode(tt.flag, tt.localBin, tt.state); got != tt.want {
				t.Fatalf("shouldAdoptRemoteBinMode(%v,%q,%+v) = %v, want %v", tt.flag, tt.localBin, tt.state, got, tt.want)
			}
		})
	}
}

// TestRemoteBinChanged pins the bridge-restart decision for package-managed
// hosts: needsUpload is permanently false on that path, so a package-manager
// upgrade of the remote binary must be detected by hash instead — otherwise
// deploy.json records the new hash while the x11-bridge keeps running the old
// executable.
func TestRemoteBinChanged(t *testing.T) {
	t.Parallel()
	info := &shim.RemoteBinaryInfo{Hash: "sha256:new"}

	tests := []struct {
		name  string
		exist *shim.RemoteBinaryInfo
		prior *shim.DeployState
		want  bool
	}{
		{"nil existing (upload path) -> not the remote-bin case", nil, &shim.DeployState{BinaryHash: "sha256:new"}, false},
		{"same hash -> unchanged", info, &shim.DeployState{BinaryHash: "sha256:new"}, false},
		{"package manager upgraded the binary", info, &shim.DeployState{BinaryHash: "sha256:old"}, true},
		{"no prior state -> fail toward restart", info, nil, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := remoteBinChanged(tt.exist, tt.prior); got != tt.want {
				t.Fatalf("remoteBinChanged(%+v,%+v) = %v, want %v", tt.exist, tt.prior, got, tt.want)
			}
		})
	}
}
