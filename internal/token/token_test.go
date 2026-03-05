package token

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateAndValidate(t *testing.T) {
	m := NewManager(1 * time.Hour)

	s, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(s.Token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d", len(s.Token))
	}

	if err := m.Validate(s.Token); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestValidateWrongToken(t *testing.T) {
	m := NewManager(1 * time.Hour)

	if _, err := m.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	err := m.Validate("wrong-token")
	if err != ErrTokenInvalid {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestValidateExpired(t *testing.T) {
	m := NewManager(1 * time.Millisecond)

	s, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	err = m.Validate(s.Token)
	if err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestValidateNoSession(t *testing.T) {
	m := NewManager(1 * time.Hour)

	err := m.Validate("any-token")
	if err != ErrTokenInvalid {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestCurrent(t *testing.T) {
	m := NewManager(1 * time.Hour)

	if m.Current() != nil {
		t.Fatal("expected nil before Generate")
	}

	s, _ := m.Generate()
	cur := m.Current()
	if cur == nil {
		t.Fatal("expected non-nil after Generate")
	}
	if cur.Token != s.Token {
		t.Fatal("Current token mismatch")
	}
}

func TestWriteAndReadTokenFile(t *testing.T) {
	// Use temp dir to avoid polluting real ~/.cache
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	m := NewManager(1 * time.Hour)
	s, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	path, err := WriteTokenFile(s.Token)
	if err != nil {
		t.Fatalf("WriteTokenFile failed: %v", err)
	}
	t.Logf("Token written to: %s", path)

	read, err := ReadTokenFile()
	if err != nil {
		t.Fatalf("ReadTokenFile failed: %v", err)
	}
	if read != s.Token {
		t.Fatalf("token mismatch: wrote %q, read %q", s.Token, read)
	}
}
