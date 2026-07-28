package shim

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const inspectRemoteBinaryCommand = `binary=$(command -v cc-clip) || exit 127
test -x "$binary" || exit 126
binary_version=$("$binary" version) || exit $?
if command -v sha256sum >/dev/null 2>&1; then
    binary_hash=$(sha256sum "$binary") || exit $?
elif command -v shasum >/dev/null 2>&1; then
    binary_hash=$(shasum -a 256 "$binary") || exit $?
else
    exit 127
fi
binary_hash=${binary_hash%% *}
printf '%s\n%s\nsha256:%s\n' "$binary" "$binary_version" "$binary_hash"`

// RemoteBinaryInfo describes an existing cc-clip executable on a remote host.
type RemoteBinaryInfo struct {
	Path    string
	Hash    string
	Version string
}

// Command returns the remote binary path as a safely quoted shell token.
func (i *RemoteBinaryInfo) Command() string {
	return shSingleQuote(i.Path)
}

// InspectRemoteBinary resolves and fingerprints cc-clip on the remote PATH.
func InspectRemoteBinary(session RemoteExecutor) (*RemoteBinaryInfo, error) {
	out, err := session.Exec(inspectRemoteBinaryCommand)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to inspect remote cc-clip; ensure it is installed and executable: %w",
			err,
		)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		return nil, fmt.Errorf("unexpected inspection output: got %d lines", len(lines))
	}

	path := strings.TrimSpace(lines[0])
	versionLine := strings.TrimSpace(lines[1])
	hash := strings.TrimSpace(lines[2])
	if path == "" {
		return nil, fmt.Errorf("unexpected inspection output: binary path is empty")
	}
	if !strings.HasPrefix(versionLine, "cc-clip ") {
		return nil, fmt.Errorf("unexpected version output: %q", versionLine)
	}
	binaryVersion := strings.TrimSpace(strings.TrimPrefix(versionLine, "cc-clip "))
	if binaryVersion == "" {
		return nil, fmt.Errorf("unexpected version output: %q", versionLine)
	}
	if err := validateRemoteBinaryHash(hash); err != nil {
		return nil, err
	}

	return &RemoteBinaryInfo{
		Path:    path,
		Hash:    hash,
		Version: binaryVersion,
	}, nil
}

func validateRemoteBinaryHash(hash string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(hash, prefix) {
		return fmt.Errorf("invalid remote binary hash: %q", hash)
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(hash, prefix))
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid remote binary hash: %q", hash)
	}
	return nil
}
