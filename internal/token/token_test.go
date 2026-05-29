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

func TestLoadOrGenerate_LogsWarningOnReadError(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	// Capture warnings emitted during fallthrough-to-Generate.
	var warnings []string
	prev := warnf
	warnf = func(format string, args ...any) {
		warnings = append(warnings, format)
	}
	defer func() { warnf = prev }()

	// Write a corrupt/old-format token file: a real READ ERROR (not a missing file).
	dir, err := TokenDir()
	if err != nil {
		t.Fatalf("TokenDir failed: %v", err)
	}
	path := filepath.Join(dir, "session.token")
	if err := os.WriteFile(path, []byte("only-one-line-no-expiry\n"), 0600); err != nil {
		t.Fatalf("write corrupt token file failed: %v", err)
	}

	ttl := 1 * time.Hour
	m := NewManager(ttl)
	_, reused, err := m.LoadOrGenerate(ttl)
	if err != nil {
		t.Fatalf("LoadOrGenerate failed: %v", err)
	}
	if reused {
		t.Fatal("expected fresh token (file is corrupt), but token was reused")
	}
	if len(warnings) == 0 {
		t.Fatal("expected a warning to be logged when rotating due to a read error, got none")
	}
}

func TestLoadOrGenerate_NoWarningOnMissingFile(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	var warnings []string
	prev := warnf
	warnf = func(format string, args ...any) {
		warnings = append(warnings, format)
	}
	defer func() { warnf = prev }()

	// No token file exists: this is the normal first-run path, not a defect.
	ttl := 1 * time.Hour
	m := NewManager(ttl)
	_, reused, err := m.LoadOrGenerate(ttl)
	if err != nil {
		t.Fatalf("LoadOrGenerate failed: %v", err)
	}
	if reused {
		t.Fatal("expected fresh token on missing file, but token was reused")
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warning on missing file, got: %v", warnings)
	}
}

func TestWriteTokenFile_AtomicNoLingeringTempAndMode0600(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	expiry := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	path, err := WriteTokenFile("atomic-token", expiry)
	if err != nil {
		t.Fatalf("WriteTokenFile failed: %v", err)
	}

	// Content must be the two-line format.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	want := "atomic-token\n" + expiry.Format(time.RFC3339) + "\n"
	if string(data) != want {
		t.Fatalf("content mismatch: wrote %q, got %q", want, string(data))
	}

	// Mode must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected mode 0600, got %o", perm)
	}

	// No temp file should linger in the directory.
	dir, err := TokenDir()
	if err != nil {
		t.Fatalf("TokenDir failed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "session.token" {
			t.Fatalf("unexpected leftover file in token dir: %q", e.Name())
		}
	}
}

func TestWriteTokenFile_OverwriteIsAtomicAndComplete(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "cc-clip")
	TokenDirOverride = tmpDir
	defer func() { TokenDirOverride = "" }()

	expiry1 := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	if _, err := WriteTokenFile("first-token", expiry1); err != nil {
		t.Fatalf("first WriteTokenFile failed: %v", err)
	}

	// Overwrite with a new token; the replacement must be complete (rename, not truncate-then-write).
	expiry2 := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	if _, err := WriteTokenFile("second-token", expiry2); err != nil {
		t.Fatalf("second WriteTokenFile failed: %v", err)
	}

	tok, gotExpiry, err := ReadTokenFileWithExpiry()
	if err != nil {
		t.Fatalf("ReadTokenFileWithExpiry failed: %v", err)
	}
	if tok != "second-token" {
		t.Fatalf("expected overwritten token %q, got %q", "second-token", tok)
	}
	if !gotExpiry.Equal(expiry2) {
		t.Fatalf("expected expiry %v, got %v", expiry2, gotExpiry)
	}

	// Still no lingering temp files after overwrite.
	dir, err := TokenDir()
	if err != nil {
		t.Fatalf("TokenDir failed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "session.token" {
			t.Fatalf("unexpected leftover file in token dir after overwrite: %q", e.Name())
		}
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
