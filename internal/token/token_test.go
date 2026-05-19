package token

import (
	"os"
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

func TestConstantTimeTokenEqual(t *testing.T) {
	if !constantTimeTokenEqual("abc123", "abc123") {
		t.Fatal("expected equal tokens to match")
	}
	if constantTimeTokenEqual("abc123", "abc124") {
		t.Fatal("expected different tokens of equal length to be rejected")
	}
	if constantTimeTokenEqual("abc123", "abc1234") {
		t.Fatal("expected different-length tokens to be rejected")
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
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	m := NewManager(1 * time.Hour)
	s, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	path, err := WriteTokenFile(s.Token, s.ExpiresAt)
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

func TestReadTokenFileWithExpiry(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	expiry := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	_, err := WriteTokenFile("test-token-abc", expiry)
	if err != nil {
		t.Fatalf("WriteTokenFile failed: %v", err)
	}

	tok, expiresAt, err := ReadTokenFileWithExpiry()
	if err != nil {
		t.Fatalf("ReadTokenFileWithExpiry failed: %v", err)
	}
	if tok != "test-token-abc" {
		t.Fatalf("token mismatch: expected %q, got %q", "test-token-abc", tok)
	}
	if !expiresAt.Equal(expiry) {
		t.Fatalf("expiry mismatch: expected %v, got %v", expiry, expiresAt)
	}
}

func TestReadTokenFileWithExpiry_OldFormat(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	// Simulate old format: single line, no expiry
	dir, err := TokenDir()
	if err != nil {
		t.Fatalf("TokenDir failed: %v", err)
	}
	path := filepath.Join(dir, "session.token")
	if err := os.WriteFile(path, []byte("old-format-token\n"), 0600); err != nil {
		t.Fatalf("write old format file failed: %v", err)
	}

	// ReadTokenFile should still work (backward compat)
	tok, err := ReadTokenFile()
	if err != nil {
		t.Fatalf("ReadTokenFile with old format failed: %v", err)
	}
	if tok != "old-format-token" {
		t.Fatalf("expected %q, got %q", "old-format-token", tok)
	}

	// ReadTokenFileWithExpiry should return error for old format
	_, _, err = ReadTokenFileWithExpiry()
	if err == nil {
		t.Fatal("expected error for old format token file, got nil")
	}
}

func TestLoadOrGenerate_ReusesValidToken(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	ttl := 1 * time.Hour

	// Write a valid token file
	expiry := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	_, err := WriteTokenFile("existing-valid-token", expiry)
	if err != nil {
		t.Fatalf("WriteTokenFile failed: %v", err)
	}

	m := NewManager(ttl)
	session, reused, err := m.LoadOrGenerate(ttl)
	if err != nil {
		t.Fatalf("LoadOrGenerate failed: %v", err)
	}
	if !reused {
		t.Fatal("expected token to be reused, but it was not")
	}
	if session.Token != "existing-valid-token" {
		t.Fatalf("expected reused token %q, got %q", "existing-valid-token", session.Token)
	}
	if !session.ExpiresAt.Equal(expiry) {
		t.Fatalf("expected expiry %v, got %v", expiry, session.ExpiresAt)
	}

	// Validate should accept the loaded token
	if err := m.Validate("existing-valid-token"); err != nil {
		t.Fatalf("Validate failed on reused token: %v", err)
	}
}

func TestLoadOrGenerate_ExpiredToken(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	ttl := 1 * time.Hour

	// Write an expired token file
	expiry := time.Now().Add(-1 * time.Minute)
	_, err := WriteTokenFile("expired-token", expiry)
	if err != nil {
		t.Fatalf("WriteTokenFile failed: %v", err)
	}

	m := NewManager(ttl)
	session, reused, err := m.LoadOrGenerate(ttl)
	if err != nil {
		t.Fatalf("LoadOrGenerate failed: %v", err)
	}
	if reused {
		t.Fatal("expected new token generation, but token was reused")
	}
	if session.Token == "expired-token" {
		t.Fatal("expected a different token, got the expired one")
	}
	if len(session.Token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d chars", len(session.Token))
	}
}

func TestLoadOrGenerate_MissingFile(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	ttl := 1 * time.Hour

	// No token file exists
	m := NewManager(ttl)
	session, reused, err := m.LoadOrGenerate(ttl)
	if err != nil {
		t.Fatalf("LoadOrGenerate failed: %v", err)
	}
	if reused {
		t.Fatal("expected new token generation, but token was reused")
	}
	if len(session.Token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d chars", len(session.Token))
	}
}

func TestLoadOrGenerate_OldFormatTreatedAsExpired(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	ttl := 1 * time.Hour

	// Write old format (single line, no expiry)
	dir, err := TokenDir()
	if err != nil {
		t.Fatalf("TokenDir failed: %v", err)
	}
	path := filepath.Join(dir, "session.token")
	if err := os.WriteFile(path, []byte("old-single-line-token\n"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	m := NewManager(ttl)
	session, reused, err := m.LoadOrGenerate(ttl)
	if err != nil {
		t.Fatalf("LoadOrGenerate failed: %v", err)
	}
	if reused {
		t.Fatal("expected new token generation for old format, but token was reused")
	}
	if session.Token == "old-single-line-token" {
		t.Fatal("expected a different token, got the old one")
	}
	if len(session.Token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d chars", len(session.Token))
	}
}

func TestValidateSlidingExpiration(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	ttl := 1 * time.Hour

	m := NewManager(ttl)
	s, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Manually set expiry to just under half TTL remaining (should trigger renewal)
	m.mu.Lock()
	m.session.ExpiresAt = time.Now().Add(ttl/2 - 1*time.Minute)
	m.mu.Unlock()

	expiryBefore := m.Current().ExpiresAt

	if err := m.Validate(s.Token); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	expiryAfter := m.Current().ExpiresAt
	if !expiryAfter.After(expiryBefore) {
		t.Fatalf("expected expiry to be extended: before=%v, after=%v", expiryBefore, expiryAfter)
	}

	// Verify token file was updated
	_, fileExpiry, err := ReadTokenFileWithExpiry()
	if err != nil {
		t.Fatalf("ReadTokenFileWithExpiry failed: %v", err)
	}
	if !fileExpiry.Equal(expiryAfter.Truncate(time.Second)) {
		t.Fatalf("token file expiry mismatch: expected %v, got %v", expiryAfter.Truncate(time.Second), fileExpiry)
	}
}

func TestValidateNoSlidingWhenFresh(t *testing.T) {
	m := NewManager(1 * time.Hour)
	s, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	expiryBefore := m.Current().ExpiresAt

	if err := m.Validate(s.Token); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	expiryAfter := m.Current().ExpiresAt
	if !expiryAfter.Equal(expiryBefore) {
		t.Fatalf("expiry should not change when remaining > ttl/2: before=%v, after=%v", expiryBefore, expiryAfter)
	}
}

func TestRotateToken_ForcesNewGeneration(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	ttl := 1 * time.Hour

	// Write a valid token file
	expiry := time.Now().Add(30 * time.Minute)
	_, err := WriteTokenFile("should-not-be-reused", expiry)
	if err != nil {
		t.Fatalf("WriteTokenFile failed: %v", err)
	}

	// Simulate --rotate-token: use Generate() directly instead of LoadOrGenerate()
	m := NewManager(ttl)
	session, err := m.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if session.Token == "should-not-be-reused" {
		t.Fatal("expected a different token when rotating, got the existing one")
	}
	if len(session.Token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d chars", len(session.Token))
	}
}
