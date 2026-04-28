// Package hosts persists a per-user registry of SSH targets this machine
// has connected to with cc-clip.
//
// The registry is intentionally minimal:
//
//   - Host keys are the literal SSH target a user typed on the command line
//     (for example `myserver` or `user@venus`). We do NOT canonicalize or
//     resolve via SSH config — different aliases that point to the same
//     remote are intentionally different entries, because the shim install
//     and Codex support are driven by the alias string, not the resolved
//     host.
//
//   - The `Codex` flag is sticky. A plain `cc-clip connect myserver` that
//     succeeds after a previous `cc-clip connect myserver --codex` must not
//     silently mark the host as "no longer using Codex". The flag is only
//     cleared by an explicit `uninstall --codex --host myserver`.
//
// File layout: `~/.cache/cc-clip/hosts.json`, mode 0600, atomic replace via
// tempfile-rename so a crash mid-write cannot leave a half-written registry.
package hosts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	registryDir  = ".cache/cc-clip"
	registryFile = "hosts.json"
	fileMode     = 0o600
	dirMode      = 0o700
)

// Entry is the per-host state we track. Intentionally small so that drifting
// the registry schema is rare.
type Entry struct {
	LastConnected       time.Time `json:"last_connected"`
	LastDeployedVersion string    `json:"last_deployed_version,omitempty"`
	Codex               bool      `json:"codex"`
}

// Registry is the on-disk data. Exposed so that callers can range over the
// map directly if they want (e.g., `cc-clip update`'s reminder printer).
type Registry struct {
	Hosts map[string]Entry `json:"hosts"`
}

// RegistryPathOverride redirects Path() to a test-controlled location.
// Tests set this in TestMain or per-test setup; production code leaves it "".
var RegistryPathOverride string

// NamedEntry is what callers iterate when they need both the host key
// and the per-host state in stable order. Returned by (*Registry).Sorted().
type NamedEntry struct {
	Host string
	Entry
}

// Path returns the path to the registry file for the current user, or the
// override path if set. Returns an error only when the home directory cannot
// be resolved (e.g. HOME unset and no passwd entry).
func Path() (string, error) {
	if RegistryPathOverride != "" {
		return RegistryPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, registryDir, registryFile), nil
}

// Load reads the registry from disk. A missing file returns an empty
// registry with no error — there is no migration, first-run is just empty.
// Corrupt JSON returns an error so the caller can surface it rather than
// silently forget every host.
func Load() (*Registry, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Registry{Hosts: map[string]Entry{}}, nil
		}
		return nil, fmt.Errorf("read hosts registry %s: %w", path, err)
	}
	r := &Registry{Hosts: map[string]Entry{}}
	if len(data) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse hosts registry %s: %w", path, err)
	}
	if r.Hosts == nil {
		r.Hosts = map[string]Entry{}
	}
	return r, nil
}

// Save writes the registry atomically: write to a sibling tempfile with
// mode 0600, then rename over the target. A crash between write and rename
// leaves the previous registry intact.
func (r *Registry) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), registryFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("create registry tempfile: %w", err)
	}
	tmpName := tmp.Name()
	// best-effort cleanup if we bail before rename
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod registry tempfile: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write registry tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close registry tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename registry tempfile: %w", err)
	}
	return nil
}

// UpsertConnect records a successful `cc-clip connect` / `setup` for this
// host. The Codex flag is sticky: a successful non-codex connect does not
// clear an existing Codex=true. Only ClearCodex() sets it back to false.
//
// `version` is the cc-clip version that performed the deploy (typically
// the value in `main.version`). An empty version leaves the previously
// recorded version untouched so the function is safe to call even when
// the caller does not know the version.
func (r *Registry) UpsertConnect(host, version string, codex bool) {
	if r.Hosts == nil {
		r.Hosts = map[string]Entry{}
	}
	e := r.Hosts[host]
	e.LastConnected = time.Now().UTC()
	if version != "" {
		e.LastDeployedVersion = version
	}
	if codex {
		e.Codex = true
	}
	r.Hosts[host] = e
}

// ClearCodex clears the sticky Codex flag for a host. Used by
// `uninstall --codex --host <host>` to reflect that Codex support was
// explicitly torn down on that remote. Returns true if an entry existed.
func (r *Registry) ClearCodex(host string) bool {
	e, ok := r.Hosts[host]
	if !ok {
		return false
	}
	e.Codex = false
	r.Hosts[host] = e
	return true
}

// Forget removes a host entry. Returns true if the entry existed.
func (r *Registry) Forget(host string) bool {
	_, ok := r.Hosts[host]
	if !ok {
		return false
	}
	delete(r.Hosts, host)
	return true
}

// Sorted returns host entries ordered by host name so CLI output is stable.
func (r *Registry) Sorted() []NamedEntry {
	names := make([]string, 0, len(r.Hosts))
	for h := range r.Hosts {
		names = append(names, h)
	}
	sort.Strings(names)
	out := make([]NamedEntry, len(names))
	for i, h := range names {
		out[i] = NamedEntry{Host: h, Entry: r.Hosts[h]}
	}
	return out
}

// FormatRedeployReminder writes a per-host `cc-clip connect` command list to w
// for use after a self-update. Returns false (and writes nothing) when the
// registry is empty so the caller can fall back to a generic reminder.
func (r *Registry) FormatRedeployReminder(w io.Writer) bool {
	entries := r.Sorted()
	if len(entries) == 0 {
		return false
	}
	fmt.Fprintln(w, "* Redeploy to every remote host you use with cc-clip:")
	for _, e := range entries {
		flags := "--force"
		if e.Codex {
			flags = "--codex --force"
		}
		fmt.Fprintf(w, "    cc-clip connect %s %s\n", e.Host, flags)
	}
	return true
}
