package tunnelmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/tunnel"
)

// schemaVersion is bumped only by a change that alters how the record must
// be read. Loading a file written by a different schema version is an error,
// never a best-effort guess — the supervisor refuses to run rather than
// misreading state it does not understand.
const schemaVersion = 1

const (
	stateDir      = ".cache/cc-clip/tunnels"
	stateFileMode = 0o600
	stateDirMode  = 0o700
)

// Spec is the tunnel configuration the supervisor runs. It is what Phase 1A
// actually needs to build the ssh command and verify the expected daemon
// identity; anything more arrives with later phases and real callers.
type Spec struct {
	// Host is the literal SSH target the user typed (alias or user@host).
	// No SSH-config resolution is performed — this matches the hosts.json
	// keying convention.
	Host string `json:"host"`

	// Port is both the remote bind port of the managed reverse forward and
	// the local daemon port it targets. cc-clip uses one port for both today
	// (RemoteForward 18339 127.0.0.1:18339).
	Port int `json:"port"`

	// ExpectedInstanceID is the installation identity that must answer through
	// this managed forward before the supervisor may report healthy.
	ExpectedInstanceID string `json:"expected_instance_id"`
}

// DescribeSpecDifferences compares only persisted configuration fields, in a
// fixed order. Callers supply an explicit request or validated desired spec;
// this function never resolves environment defaults or probes a daemon.
func DescribeSpecDifferences(persisted, requested Spec) string {
	var differences []string
	if persisted.Host != requested.Host {
		differences = append(differences, fmt.Sprintf("host: persisted=%q, requested=%q", persisted.Host, requested.Host))
	}
	if persisted.Port != requested.Port {
		differences = append(differences, fmt.Sprintf("port: persisted=%d, requested=%d", persisted.Port, requested.Port))
	}
	if persisted.ExpectedInstanceID != requested.ExpectedInstanceID {
		differences = append(differences, fmt.Sprintf("expected_instance_id: persisted=%q, requested=%q", persisted.ExpectedInstanceID, requested.ExpectedInstanceID))
	}
	return strings.Join(differences, "; ")
}

// Runtime is the supervisor's persisted runtime state. It is rebuildable
// observability, not an authoritative desired-state record.
type Runtime struct {
	State State `json:"state"`

	// RemoteState is the raw outcome of the last remote probe, expressed in
	// the canonical internal/tunnel classification. Empty when no probe has
	// completed. It is observability only — healthiness is decided by
	// Runtime.State, which is healthy only when RemoteState was ok.
	RemoteState tunnel.RemoteTunnelState `json:"remote_state,omitempty"`

	// LastError is the reason for the most recent non-healthy state. It may
	// name the SSH target, which is why the state file is mode 0600.
	LastError string `json:"last_error,omitempty"`

	// ConsecutiveStartFailures counts consecutive children that failed before
	// the starting gate. It drives the crash-loop breaker.
	ConsecutiveStartFailures int `json:"consecutive_start_failures,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

// Record is the on-disk state for one tunnel: the spec the supervisor runs
// plus its latest runtime snapshot. One file per literal SSH target under
// ~/.cache/cc-clip/tunnels/, mode 0600, atomic tempfile-rename writes.
type Record struct {
	SchemaVersion int     `json:"schema_version"`
	Spec          Spec    `json:"spec"`
	Runtime       Runtime `json:"runtime"`
}

// Store reads and writes the state file for one tunnel.
type Store struct {
	path string
}

// DefaultStorePath returns the state file path for the given literal SSH
// target: ~/.cache/cc-clip/tunnels/<encoded-host>.json.
func DefaultStorePath(host string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, stateDir, encodeHostKey(host)+".json"), nil
}

// NewStoreAt returns a store rooted at an explicit path. Tests use it to
// keep state out of the real user cache directory; production callers use
// DefaultStorePath.
func NewStoreAt(path string) *Store {
	return &Store{path: path}
}

// Path returns the underlying state file path.
func (s *Store) Path() string {
	return s.path
}

// Reset replaces rebuildable Phase 1A state after an explicit operator
// request. It is the recovery path for a corrupt runtime or an opened circuit
// breaker; normal supervisor starts never reset state implicitly.
func (s *Store) Reset(spec Spec) error {
	return s.Save(&Record{
		SchemaVersion: schemaVersion,
		Spec:          spec,
		Runtime:       Runtime{State: StateUnknown, UpdatedAt: time.Now().UTC()},
	})
}

// Load reads and validates the state file. A missing file returns
// fs.ErrNotExist so callers can distinguish first-run from corruption.
// Everything that cannot be validated — corrupt JSON, a foreign
// schema_version, an unknown supervisor state, an unknown remote probe
// state, or a nonsensical spec — is an error. Nothing is silently reset or
// coerced into a healthy state.
func (s *Store) Load() (*Record, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read tunnel state %s: %w", s.path, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse tunnel state %s: %w", s.path, err)
	}
	if err := rec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tunnel state %s: %w", s.path, err)
	}
	return &rec, nil
}

// Save writes the record atomically: tempfile in the same directory with
// mode 0600, then rename over the target. A crash mid-write leaves the
// previous state intact.
func (s *Store) Save(rec *Record) error {
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid tunnel state: %w", err)
	}
	if err := ensurePrivateDir(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("secure tunnel state dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tunnel state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create tunnel state tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(stateFileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod tunnel state tempfile: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write tunnel state tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync tunnel state tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tunnel state tempfile: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("rename tunnel state tempfile: %w", err)
	}
	return nil
}

// Validate enforces the fail-closed contract on any record, whether it came
// from disk or is about to be written.
func (r *Record) Validate() error {
	if r.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", r.SchemaVersion, schemaVersion)
	}
	if r.Spec.Host == "" {
		return fmt.Errorf("spec.host is empty")
	}
	if r.Spec.Port < 1 || r.Spec.Port > 65535 {
		return fmt.Errorf("spec.port %d out of range", r.Spec.Port)
	}
	if r.Spec.ExpectedInstanceID == "" {
		return fmt.Errorf("spec.expected_instance_id is empty")
	}
	if _, err := ParseState(string(r.Runtime.State)); err != nil {
		return err
	}
	if rs := r.Runtime.RemoteState; rs != "" && !validRemoteState(rs) {
		return fmt.Errorf("unknown remote probe state %q", rs)
	}
	return nil
}

// validRemoteState checks a remote probe outcome against the canonical
// internal/tunnel classification. The switch reuses the tunnel package's own
// constants so the two can never drift.
func validRemoteState(rs tunnel.RemoteTunnelState) bool {
	switch rs {
	case tunnel.RemoteTunnelOK, tunnel.RemoteTunnelDown, tunnel.RemoteTunnelStale,
		tunnel.RemoteTunnelUnverified, tunnel.RemoteTunnelUnknown:
		return true
	default:
		return false
	}
}

// encodeHostKey maps a literal SSH target to a single safe path component.
// Every byte outside [A-Za-z0-9._-] is percent-encoded, so distinct targets
// ("user@a:1" vs "user@a_1") map to distinct files and the mapping is
// reversible.
func encodeHostKey(host string) string {
	var b strings.Builder
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
