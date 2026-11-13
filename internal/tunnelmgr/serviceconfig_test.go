package tunnelmgr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func serviceConfig(host string) ServiceConfig {
	return ServiceConfig{SchemaVersion: serviceSchemaVersion, ID: TunnelID(host), Enabled: true,
		Spec: Spec{Host: host, Port: 18399, ExpectedInstanceID: "instance-123"}}
}

func TestServiceConfigRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private LaunchAgent specs use Unix permissions")
	}
	s := NewServiceStoreAt(filepath.Join(t.TempDir(), "tunnel-specs"))
	first, second := serviceConfig("user@host:22"), serviceConfig("user_host_22")
	for _, c := range []ServiceConfig{first, second} {
		if err := s.Save(c); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load(c.ID)
		if err != nil || *got != c {
			t.Fatalf("round trip = %+v, %v", got, err)
		}
		path, _ := s.Path(c.ID)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("file mode: %v, %v", info, err)
		}
	}
	info, _ := os.Stat(s.dir)
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode = %o", info.Mode().Perm())
	}
	first.Enabled = false
	if err := s.Save(first); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(second.ID)
	if err != nil || !got.Enabled {
		t.Fatalf("other host changed: %+v, %v", got, err)
	}
	store, err := s.RuntimeStore(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.Path(first.ID)
	if store.Path() == path || !strings.Contains(store.Path(), "tunnel-runtime") {
		t.Fatal("runtime overlaps desired configuration")
	}
}

func TestServiceConfigRejectsInvalidRecords(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LaunchAgent file permissions")
	}
	for name, mutate := range map[string]func(*ServiceConfig){
		"schema":      func(c *ServiceConfig) { c.SchemaVersion++ },
		"id mismatch": func(c *ServiceConfig) { c.ID = TunnelID("another-host") },
		"empty host":  func(c *ServiceConfig) { c.Spec.Host = ""; c.ID = TunnelID("") },
		"nul":         func(c *ServiceConfig) { c.Spec.Host = "bad\x00host"; c.ID = TunnelID(c.Spec.Host) },
		"newline":     func(c *ServiceConfig) { c.Spec.Host = "bad\nhost"; c.ID = TunnelID(c.Spec.Host) },
		"option":      func(c *ServiceConfig) { c.Spec.Host = "-oProxyCommand=bad"; c.ID = TunnelID(c.Spec.Host) },
		"port":        func(c *ServiceConfig) { c.Spec.Port = 65536 },
		"identity":    func(c *ServiceConfig) { c.Spec.ExpectedInstanceID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			s := NewServiceStoreAt(t.TempDir())
			c := serviceConfig("host")
			mutate(&c)
			if err := s.Save(c); err == nil {
				t.Fatal("invalid spec saved")
			}
			path, _ := s.Path(c.ID)
			data, _ := json.Marshal(c)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(c.ID); err == nil {
				t.Fatal("invalid spec loaded")
			}
		})
	}
	for _, id := range []string{"", "../host", strings.Repeat("g", 64), strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		if _, err := NewServiceStoreAt(t.TempDir()).Path(id); err == nil {
			t.Fatalf("accepted id %q", id)
		}
	}
}

func TestServiceConfigRejectsCorruptAndMisnamedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LaunchAgent file permissions")
	}
	s := NewServiceStoreAt(t.TempDir())
	c := serviceConfig("host")
	path, _ := s.Path(c.ID)
	if err := os.WriteFile(path, []byte("{bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(c.ID); err == nil {
		t.Fatal("accepted corrupt JSON")
	}
	other := serviceConfig("another")
	data, _ := json.Marshal(other)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(c.ID); err == nil {
		t.Fatal("accepted mismatched filename")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(c.ID); err == nil {
		t.Fatal("accepted public spec")
	}
}

func TestServiceConfigRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture")
	}
	dir := t.TempDir()
	s := NewServiceStoreAt(filepath.Join(dir, "specs"))
	c := serviceConfig("host")
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	path, _ := s.Path(c.ID)
	other := serviceConfig("other")
	otherPath, _ := s.Path(other.ID)
	if err := os.Symlink(path, otherPath); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(other.ID); err == nil {
		t.Fatal("accepted symlink spec")
	}
	link := filepath.Join(dir, "linked-specs")
	if err := os.Symlink(s.dir, link); err != nil {
		t.Fatal(err)
	}
	if err := NewServiceStoreAt(link).Save(c); err == nil {
		t.Fatal("accepted symlink spec directory")
	}
}
