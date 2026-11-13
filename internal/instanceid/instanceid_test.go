package instanceid

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestLoadOrCreateIsStableAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	first, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("instance id not stable: first=%q second=%q", first, second)
	}
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("instance-id mode = %o, want 600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatalf("unexpected files after instance-id creation: %v", entries)
	}
}

func TestLoadOrCreateConcurrentCallersShareIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	const callers = 32

	start := make(chan struct{})
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := LoadOrCreate(dir)
			ids <- id
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("LoadOrCreate failed: %v", err)
		}
	}
	var want string
	for id := range ids {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("concurrent callers returned different IDs: got %q, want %q", id, want)
		}
	}
}

func TestLoadOrCreateRejectsCorruptIdentityWithoutReplacingIt(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"empty", ""},
		{"partial", "partial\n"},
		{"non-hex", strings.Repeat("z", 64)},
		{"short-hex", strings.Repeat("a", 62)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := tc.content
			dir := t.TempDir()
			path := filepath.Join(dir, fileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, load := range []func(string) (string, error){Load, LoadOrCreate} {
				id, err := load(dir)
				if err == nil || id != "" {
					t.Fatalf("corrupt identity accepted: id=%q err=%v", id, err)
				}
				for _, hint := range []string{strconv.Quote(path), "stop the daemon and managed tunnels", "restore a valid backup", "move this file aside and restart", "disabling/re-enabling", "--reset"} {
					if !strings.Contains(err.Error(), hint) {
						t.Fatalf("missing repair hint %q: %v", hint, err)
					}
				}
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != content || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
				t.Fatal("corrupt identity was replaced or modified")
			}
		})
	}
}

func TestLoadOrCreateHardLinkFailureLeavesIdentityUnavailable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	linkErr := errors.New("filesystem does not support hard links")
	id, err := loadOrCreate(dir, func(old, new string) error {
		if new != path {
			t.Fatalf("publish path = %q, want %q", new, path)
		}
		if _, err := read(old); err != nil {
			t.Fatalf("temporary identity is incomplete at publication: %v", err)
		}
		return linkErr
	})
	if id != "" || !errors.Is(err, linkErr) {
		t.Fatalf("hard-link failure: id=%q err=%v", id, err)
	}
	if !strings.Contains(err.Error(), strconv.Quote(path)) || !strings.Contains(err.Error(), "hard-link support") {
		t.Fatalf("missing filesystem guidance: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("hard-link failure published an identity or left temporary files: %v", entries)
	}
}

func TestLoadOrCreatePreservesExistingSessionToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "session.token")
	wantToken := []byte("existing-token\n2030-01-01T00:00:00Z\n")
	if err := os.WriteFile(tokenPath, wantToken, 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("instance id changed across daemon-style restarts: first=%q second=%q", first, second)
	}

	gotToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotToken) != string(wantToken) {
		t.Fatalf("existing session token changed: got %q, want %q", gotToken, wantToken)
	}
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("session token mode = %o, want 600", got)
	}
}
