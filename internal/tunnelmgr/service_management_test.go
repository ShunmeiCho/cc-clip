//go:build !windows

package tunnelmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagementLockSerializesAndReleases(t *testing.T) {
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "specs"))
	unlock, err := s.LockManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.LockManagement(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock = %v", err)
	}
	unlock()
	unlocked, err := s.LockManagement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	unlocked()
}

func TestServiceListAndLoadRemainReadOnly(t *testing.T) {
	root := t.TempDir()
	s := NewServiceStoreAt(filepath.Join(root, "specs"))
	if ids, err := s.ListIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("empty listing: %v %v", ids, err)
	}
	c := serviceConfig("host")
	if _, err := s.Load(c.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing load: %v", err)
	}
	if _, err := os.Stat(s.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created a directory")
	}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(c.ID); err == nil {
		t.Fatal("public directory accepted")
	}
	if _, err := s.ListIDs(); err == nil {
		t.Fatal("listing accepted public directory")
	}
	info, _ := os.Stat(s.dir)
	if info.Mode().Perm() != 0755 {
		t.Fatal("read repaired permissions")
	}
}

func TestRuntimeReadsRejectPublicAndSymlinkDirectoriesWithoutRepair(t *testing.T) {
	for _, mode := range []string{"public", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			s := NewServiceStoreAt(filepath.Join(root, "specs"))
			c := serviceConfig("host")
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
			if _, err := s.ReadRuntime(c); err == nil {
				t.Fatal("unsafe runtime directory accepted")
			}
			info, err := os.Lstat(dir)
			if err != nil || mode == "public" && info.Mode().Perm() != 0755 || mode == "symlink" && info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("read repaired runtime directory")
			}
		})
	}
}

func TestRuntimeResetRefusesCorruptionAndForeignSchema(t *testing.T) {
	for _, data := range []string{"{bad", `{"schema_version":99}`} {
		s := NewServiceStoreAt(filepath.Join(t.TempDir(), "specs"))
		c := serviceConfig("host")
		store, _ := s.RuntimeStore(c.ID)
		if err := store.Reset(c.Spec); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.Path(), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetRuntime(c); err == nil {
			t.Fatal("invalid runtime reset")
		}
		got, _ := os.ReadFile(store.Path())
		if string(got) != data {
			t.Fatal("invalid runtime changed")
		}
	}
}

func TestWaitStoppedIsBoundedByContext(t *testing.T) {
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "specs"))
	c := serviceConfig("host")
	store, _ := s.RuntimeStore(c.ID)
	unlock, err := lockService(store.Path() + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.WaitStopped(ctx, c.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown wait: %v", err)
	}
}

func TestRuntimeSaveRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "runtime")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := NewStoreAt(filepath.Join(link, "state.json")).Save(validRecord()); err == nil {
		t.Fatal("runtime write followed directory symlink")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("runtime write changed symlink target permissions")
	}
	if _, err := os.Stat(filepath.Join(target, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("runtime write changed symlink target")
	}
}
