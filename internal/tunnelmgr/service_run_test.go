//go:build !windows

package tunnelmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunServiceDisabledIgnoresStaleRuntime(t *testing.T) {
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
	c := serviceConfig("fake-host")
	c.Enabled = false
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	runtime, _ := s.RuntimeStore(c.ID)
	if err := os.MkdirAll(filepath.Dir(runtime.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime.Path(), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runService(context.Background(), s, c.ID, nil, func(Spec, *Store) *Supervisor {
		t.Fatal("disabled service created a supervisor")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(runtime.Path())
	if string(data) != "{broken" {
		t.Fatal("disabled service touched stale runtime")
	}
}

func TestRunServiceStopsOnInvalidConfiguration(t *testing.T) {
	for _, mode := range []string{"missing spec", "corrupt runtime", "mismatched runtime", "crash-loop"} {
		t.Run(mode, func(t *testing.T) {
			s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
			c := serviceConfig("fake-host")
			if mode != "missing spec" {
				if err := s.Save(c); err != nil {
					t.Fatal(err)
				}
				store, _ := s.RuntimeStore(c.ID)
				rec := &Record{SchemaVersion: schemaVersion, Spec: c.Spec, Runtime: Runtime{State: StateCrashLoop}}
				if mode == "mismatched runtime" {
					rec.Spec.Port++
				}
				if err := store.Save(rec); err != nil {
					t.Fatal(err)
				}
				if mode == "corrupt runtime" {
					if err := os.WriteFile(store.Path(), []byte("bad"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var logs strings.Builder
			err := RunService(context.Background(), s, c.ID, func(format string, args ...any) { fmt.Fprintf(&logs, format, args...) })
			if err != nil {
				t.Fatalf("operator-action failure must exit successfully: %v", err)
			}
			if !strings.Contains(logs.String(), "stopped") {
				t.Fatalf("missing failure log: %s", logs.String())
			}
		})
	}
}

func TestRunServiceUsesPersistedSpecAndCleansUp(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
	t.Setenv("CC_CLIP_PORT", "1")
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
	c := serviceConfig("fake-host")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	sup, _ := newTestSupervisor(t, c.Spec.Port)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- runService(ctx, s, c.ID, nil, func(spec Spec, store *Store) *Supervisor {
			sup.Spec, sup.Store, sup.Backend.Spec = spec, store, spec
			return sup
		})
	}()
	waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateHealthy }, "service health")
	if sup.Spec != c.Spec {
		t.Fatalf("spec drifted: %+v", sup.Spec)
	}
	control := sup.Backend.ControlPath()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sup.Backend.Running() {
		t.Fatal("SSH child survived shutdown")
	}
	if _, err := os.Stat(control); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control socket survived: %v", err)
	}
	store, _ := s.RuntimeStore(c.ID)
	rec, err := store.Load()
	if err != nil || rec.Runtime.State != StateStopped {
		t.Fatalf("runtime: %+v %v", rec, err)
	}
	got, err := s.Load(c.ID)
	if err != nil || *got != c {
		t.Fatalf("shutdown changed desired state: %+v %v", got, err)
	}
	unlock, err := lockService(store.Path() + ".lock")
	if err != nil {
		t.Fatalf("lifetime lock not released: %v", err)
	}
	unlock()
}

func TestRunServiceExcludesDuplicateSupervisor(t *testing.T) {
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
	c := serviceConfig("fake-host")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	store, _ := s.RuntimeStore(c.ID)
	unlock, err := lockService(store.Path() + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := runService(context.Background(), s, c.ID, nil, func(Spec, *Store) *Supervisor {
		t.Fatal("duplicate supervisor created")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other := serviceConfig("other-host")
	otherStore, _ := s.RuntimeStore(other.ID)
	otherUnlock, err := lockService(otherStore.Path() + ".lock")
	if err != nil {
		t.Fatalf("other host blocked: %v", err)
	}
	otherUnlock()
}

func TestRunServiceConfigurationErrorDoesNotRelaunch(t *testing.T) {
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
	c := serviceConfig("fake-host")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	sup, _ := newTestSupervisor(t, c.Spec.Port)
	sup.ReadLocalToken = func() (string, error) { return "", errors.New("missing local token") }
	if err := runService(context.Background(), s, c.ID, nil, func(spec Spec, store *Store) *Supervisor {
		sup.Spec, sup.Store, sup.Backend.Spec = spec, store, spec
		return sup
	}); err != nil {
		t.Fatal(err)
	}
	if sup.State() != StateConfigError || sup.Backend.Running() {
		t.Fatal("configuration error did not stop before SSH startup")
	}
}

func TestRunServiceUnexpectedRuntimeFailureReturnsError(t *testing.T) {
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
	c := serviceConfig("fake-host")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	sup, _ := newTestSupervisor(t, c.Spec.Port)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runService(context.Background(), s, c.ID, nil, func(spec Spec, _ *Store) *Supervisor {
		sup.Spec, sup.Backend.Spec = spec, spec
		// A filesystem failure after startup validation must remain a failed
		// exit, so launchd can retry rather than silently disabling the job.
		sup.Store = NewStoreAt(filepath.Join(blocked, "runtime.json"))
		return sup
	})
	if err == nil {
		t.Fatal("unexpected runtime failure was treated as operator-action stop")
	}
	if sup.Backend.Running() {
		t.Fatal("SSH started after runtime failure")
	}
}

func TestRunServiceRejectsUnsafeRuntimeDirectoryBeforeLocking(t *testing.T) {
	for _, mode := range []string{"public", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			s := NewServiceStoreAt(filepath.Join(t.TempDir(), "specs"))
			c := serviceConfig("host")
			if err := s.Save(c); err != nil {
				t.Fatal(err)
			}
			store, _ := s.RuntimeStore(c.ID)
			if err := store.Reset(c.Spec); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(store.Path())
			if mode == "public" {
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(dir, dir+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+"-original", dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := runService(context.Background(), s, c.ID, nil, func(Spec, *Store) *Supervisor {
				t.Fatal("unsafe runtime started supervisor")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(dir)
			if err != nil || mode == "public" && info.Mode().Perm() != 0755 || mode == "symlink" && info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("runtime safety check changed directory")
			}
		})
	}
}

func TestRunServiceDesiredStateStopsOwnedChild(t *testing.T) {
	for _, change := range []string{"disable", "corrupt", "reconfigure"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("FAKE_SSH_MODE", "master")
			t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
			s := NewServiceStoreAt(filepath.Join(t.TempDir(), "specs"))
			c := serviceConfig("fake-host")
			if err := s.Save(c); err != nil {
				t.Fatal(err)
			}
			sup, _ := newTestSupervisor(t, c.Spec.Port)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runService(ctx, s, c.ID, nil, func(spec Spec, store *Store) *Supervisor {
					sup.Spec, sup.Store, sup.Backend.Spec = spec, store, spec
					return sup
				})
			}()
			waitUntil(t, 5*time.Second, func() bool { return sup.State() == StateHealthy }, "healthy service")
			control := sup.Backend.ControlPath()
			switch change {
			case "disable":
				c.Enabled = false
				if err := s.Save(c); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				path, _ := s.Path(c.ID)
				if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "reconfigure":
				c.Spec.Port++
				if err := s.Save(c); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("desired state change did not stop service")
			}
			if sup.Backend.Running() {
				t.Fatal("owned SSH child survived")
			}
			for _, path := range []string{control, control + ".config"} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("owned control artifact survived: %v", err)
				}
			}
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
			defer stopCancel()
			if err := s.WaitStopped(stopCtx, c.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunServiceSSHConfigErrorStopsWithoutLaunchdRetry(t *testing.T) {
	t.Setenv("FAKE_SSH_MODE", "configfail")
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "specs"))
	c := serviceConfig("fake-host")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	sup, _ := newTestSupervisor(t, c.Spec.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runService(ctx, s, c.ID, nil, func(spec Spec, store *Store) *Supervisor {
		sup.Spec, sup.Store, sup.Backend.Spec = spec, store, spec
		return sup
	}); err != nil {
		t.Fatal(err)
	}
	if sup.State() != StateConfigError || sup.Backend.Running() {
		t.Fatal("SSH config error did not stop cleanly")
	}
}
