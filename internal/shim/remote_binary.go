package shim

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// inspectRemoteBinaryCommand resolves and fingerprints cc-clip on the remote.
//
// Resolution runs under the user's LOGIN shell first, and only falls back to a
// plain `command -v`. The order is load-bearing: every remote command goes
// through WrapRemoteShell, whose remotePathPrelude prepends the system bin
// directories ahead of the inherited $PATH (so coreutils resolve on minimal
// images — see #106). Under that PATH a plain `command -v cc-clip` can never
// see ~/.nix-profile/bin, ~/.local/bin, pipx or asdf — precisely the package
// managers #110 was filed about — and a stale /usr/local/bin copy wins
// silently. `"$SHELL" -lc` re-resolves PATH from the user's own profile, where
// those managers install themselves.
//
// The login-shell candidate is taken from the LAST output line (rc files may
// print before `command -v` does) and must itself be executable before it is
// adopted; otherwise rc noise on the last line would be mistaken for a path.
// Shells without a `command` builtin (csh) yield nothing and degrade to the
// plain fallback.
//
// Distinct exit codes per failure mode: 127 nothing found, 126 found but not
// executable, 125 no sha256 tool on the remote.
const inspectRemoteBinaryCommand = `resolved=""
if [ -n "$SHELL" ] && [ -x "$SHELL" ]; then
    candidate=$("$SHELL" -lc 'command -v cc-clip' 2>/dev/null | tail -n 1)
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
        resolved="$candidate"
    fi
fi
if [ -z "$resolved" ]; then
    resolved=$(command -v cc-clip 2>/dev/null) || true
fi
[ -n "$resolved" ] || exit 127
[ -x "$resolved" ] || exit 126
binary_version=$("$resolved" version) || exit $?
if command -v sha256sum >/dev/null 2>&1; then
    binary_hash=$(sha256sum "$resolved") || exit $?
elif command -v shasum >/dev/null 2>&1; then
    binary_hash=$(shasum -a 256 "$resolved") || exit $?
else
    exit 125
fi
binary_hash=${binary_hash%% *}
printf '%s\n%s\nsha256:%s\n' "$resolved" "$binary_version" "$binary_hash"`

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

// exitCoder is the subset of *exec.ExitError InspectRemoteBinary needs to map
// remote exit codes to actionable messages. An interface rather than a type
// assertion so SSH transport wrappers and tests can both convey codes.
type exitCoder interface{ ExitCode() int }

// InspectRemoteBinary resolves and fingerprints cc-clip on the remote PATH.
func InspectRemoteBinary(session RemoteExecutor) (*RemoteBinaryInfo, error) {
	out, err := session.Exec(inspectRemoteBinaryCommand)
	if err != nil {
		return nil, classifyInspectError(err)
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

// classifyInspectError maps the inspection script's distinct exit codes to
// messages that name the actual failure, so "no cc-clip installed" is not
// reported the same way as "the remote has no sha256 tool".
func classifyInspectError(err error) error {
	var ec exitCoder
	if errors.As(err, &ec) {
		switch ec.ExitCode() {
		case 127:
			return fmt.Errorf("no cc-clip executable found on the remote (checked the login-shell PATH, then the non-interactive PATH); install it with the remote's package manager or drop --use-remote-bin: %w", err)
		case 126:
			return fmt.Errorf("remote cc-clip was found but is not executable: %w", err)
		case 125:
			return fmt.Errorf("cannot fingerprint the remote cc-clip: the remote has neither sha256sum nor shasum: %w", err)
		}
	}
	return fmt.Errorf("failed to inspect remote cc-clip; ensure it is installed and executable: %w", err)
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
