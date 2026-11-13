package tunnelmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const serviceSchemaVersion = 1

// ServiceLocationHint applies to management commands and the LaunchAgent
// runner; the remote identity probe remains a separate remote capability.
const ServiceLocationHint = "run service commands on the local macOS machine running cc-clip serve, not on the remote host"

// ServiceConfig is authoritative desired state, separate from the rebuildable
// supervisor runtime. A LaunchAgent never derives its spec from runtime or env.
type ServiceConfig struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Enabled       bool   `json:"enabled"`
	Spec          Spec   `json:"spec"`
}

// TunnelID keys services by literal SSH target, like the Phase 1A store. Use the
// full domain-separated digest; aliases are not resolved or merged here. Ports
// and installation identity can change without changing the job's identity.
func TunnelID(host string) string {
	digest := sha256.Sum256([]byte("cc-clip/tunnel/literal-host/v1\x00" + host))
	return hex.EncodeToString(digest[:])
}

func ValidateTunnelID(id string) error {
	if len(id) != sha256.Size*2 || strings.ToLower(id) != id {
		return fmt.Errorf("invalid tunnel id %q: expected a lowercase SHA-256 digest", id)
	}
	if _, err := hex.DecodeString(id); err != nil {
		return fmt.Errorf("invalid tunnel id %q: %w", id, err)
	}
	return nil
}

func (c ServiceConfig) Validate() error {
	if c.SchemaVersion != serviceSchemaVersion {
		return fmt.Errorf("unsupported service schema_version %d (want %d)", c.SchemaVersion, serviceSchemaVersion)
	}
	if err := ValidateTunnelID(c.ID); err != nil {
		return err
	}
	if c.ID != TunnelID(c.Spec.Host) {
		return fmt.Errorf("tunnel id does not match persisted SSH target")
	}
	if strings.ContainsAny(c.Spec.Host, "\x00\r\n") || strings.HasPrefix(c.Spec.Host, "-") {
		return fmt.Errorf("invalid service SSH target")
	}
	return (&Record{SchemaVersion: schemaVersion, Spec: c.Spec, Runtime: Runtime{State: StateUnknown}}).Validate()
}

// ServiceStore persists per-host desired state under a private directory.
type ServiceStore struct {
	dir        string
	management chan struct{}
}

func DefaultServiceStore() (*ServiceStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewServiceStoreAt(filepath.Join(home, ".cache", "cc-clip", "tunnel-specs")), nil
}

func NewServiceStoreAt(dir string) *ServiceStore {
	return &ServiceStore{dir: dir, management: make(chan struct{}, 1)}
}

func (s *ServiceStore) Path(id string) (string, error) {
	if err := ValidateTunnelID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func (s *ServiceStore) Load(id string) (*ServiceConfig, error) {
	path, err := s.Path(id)
	if err != nil {
		return nil, err
	}
	if err := readPrivateDir(s.dir); err != nil {
		return nil, err
	}
	f, err := OpenPrivateFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var config ServiceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse service config %s: %w", path, err)
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid service config %s: %w", path, err)
	}
	if config.ID != id {
		return nil, fmt.Errorf("service config id does not match filename %s", path)
	}
	return &config, nil
}

func (s *ServiceStore) Save(config ServiceConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	path, err := s.Path(config.ID)
	if err != nil {
		return err
	}
	if err := ensurePrivateDir(s.dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".spec-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(stateFileMode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RuntimeStore uses a separate namespace so a foreground experiment cannot
// overwrite a background job's snapshot. Desired state is never reset here.
func (s *ServiceStore) RuntimeStore(id string) (*Store, error) {
	if err := ValidateTunnelID(id); err != nil {
		return nil, err
	}
	return NewStoreAt(filepath.Join(filepath.Dir(s.dir), "tunnel-runtime", id+".json")), nil
}
