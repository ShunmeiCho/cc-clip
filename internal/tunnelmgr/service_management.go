package tunnelmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func NewServiceConfig(spec Spec, enabled bool) (ServiceConfig, error) {
	c := ServiceConfig{SchemaVersion: serviceSchemaVersion, ID: TunnelID(spec.Host), Enabled: enabled, Spec: spec}
	return c, c.Validate()
}

// ListIDs is read-only, including when no service has been configured yet.
// Individual corrupt records are diagnosed by the caller rather than hidden.
func (s *ServiceStore) ListIDs() ([]string, error) {
	if err := readPrivateDir(s.dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if err := ValidateTunnelID(id); err != nil {
			return nil, fmt.Errorf("invalid service config filename %s: %w", e.Name(), err)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// LockManagement serializes desired-state/job mutations for this user. It is
// independent of each supervisor's lifetime lock, so disable can stop a runner.
func (s *ServiceStore) LockManagement(ctx context.Context) (func(), error) {
	// Pair the process lock with Go synchronization for callers sharing this
	// store, including cancellation while waiting. flock alone does not establish
	// a Go memory-order boundary for management callbacks and their state.
	select {
	case s.management <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	unlock, err := waitServiceLock(ctx, filepath.Join(filepath.Dir(s.dir), "tunnel-locks", "management.lock"))
	if err != nil {
		<-s.management
		return nil, err
	}
	return func() { unlock(); <-s.management }, nil
}

func (s *ServiceStore) WaitStopped(ctx context.Context, id string) error {
	store, err := s.RuntimeStore(id)
	if err != nil {
		return err
	}
	unlock, err := waitServiceLock(ctx, store.Path()+".lock")
	if err != nil {
		return fmt.Errorf("wait for tunnel supervisor shutdown: %w", err)
	}
	unlock()
	return nil
}

func waitServiceLock(ctx context.Context, path string) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unlock, err := lockService(path)
		if !errors.Is(err, ErrServiceAlreadyRunning) {
			return unlock, err
		}
		if !sleepInterruptible(ctx, 25*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
}

// ReadRuntime never repairs malformed/newer state or a mismatched spec.
func (s *ServiceStore) ReadRuntime(c ServiceConfig) (*Record, error) {
	store, err := s.RuntimeStore(c.ID)
	if err != nil {
		return nil, err
	}
	if err := readPrivateDir(filepath.Dir(store.Path())); err != nil {
		return nil, err
	}
	f, err := OpenPrivateFile(store.Path())
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse tunnel runtime: %w", err)
	}
	if err := rec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tunnel runtime: %w", err)
	}
	if rec.Spec != c.Spec {
		return nil, fmt.Errorf("runtime spec differs from desired service spec: %s; requested values are the desired service configuration; inspect configuration before restarting", DescribeSpecDifferences(rec.Spec, c.Spec))
	}
	return &rec, nil
}

func readPrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || !controlDirOwnedByCurrentUser(info) || info.Mode().Perm() != stateDirMode {
		return fmt.Errorf("%s must be a private, user-owned directory (0700)", path)
	}
	return nil
}

// OpenPrivateFile rejects symlinks, public/foreign files and path replacement
// between inspection and opening. It never repairs or changes permissions.
func OpenPrivateFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !controlDirOwnedByCurrentUser(info) || info.Mode().Perm() != stateFileMode {
		return nil, fmt.Errorf("%s must be a private, user-owned regular file (0600)", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, errors.New("private file changed while opening")
	}
	return f, nil
}

// ResetRuntime is explicit, and only allowed after the job has stopped and the
// old record has been validated. An unknown schema is never reset implicitly.
func (s *ServiceStore) ResetRuntime(c ServiceConfig) error {
	if _, err := s.ReadRuntime(c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	store, err := s.RuntimeStore(c.ID)
	if err != nil {
		return err
	}
	return store.Reset(c.Spec)
}
