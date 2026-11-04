package token

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEphemeralTokenSlidesWithoutWritingPersistentCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CC_CLIP_TOKEN_DIR", dir)
	path := filepath.Join(dir, "session.token")
	if err := os.WriteFile(path, []byte("persistent-daemon-token"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewEphemeralManager(time.Hour)
	s, err := m.Generate()
	if err != nil {
		t.Fatal(err)
	}
	m.session.ExpiresAt = time.Now().Add(time.Minute)
	if err := m.Validate(s.Token); err != nil {
		t.Fatal(err)
	}
	if time.Until(m.Current().ExpiresAt) < 50*time.Minute {
		t.Fatal("ephemeral token failed to slide")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "persistent-daemon-token" {
		t.Fatalf("temporary bridge overwrote existing credentials: %s %v", data, err)
	}
}
