package token

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrTokenExpired = errors.New("token expired")
	ErrTokenInvalid = errors.New("token invalid")
)

// warnf logs a non-fatal warning. It is a package-level indirection so tests
// can capture warnings (e.g. silent token rotation) without depending on the
// standard logger's output destination.
var warnf = func(format string, args ...any) {
	log.Printf(format, args...)
}

type Session struct {
	Token     string
	ExpiresAt time.Time
}

type Manager struct {
	mu      sync.RWMutex
	session *Session
	ttl     time.Duration
}

func NewManager(ttl time.Duration) *Manager {
	return &Manager{ttl: ttl}
}

func (m *Manager) Generate() (Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Session{}, err
	}

	s := Session{
		Token:     hex.EncodeToString(b),
		ExpiresAt: time.Now().Add(m.ttl),
	}

	m.mu.Lock()
	m.session = &s
	m.mu.Unlock()

	return s, nil
}

// LoadOrGenerate reads the existing token file and reuses it if still valid.
// If the token file is missing, unreadable, or expired, a new token is generated.
// The returned bool is true if an existing token was reused, false if newly generated.
func (m *Manager) LoadOrGenerate(ttl time.Duration) (Session, bool, error) {
	tok, expiresAt, err := ReadTokenFileWithExpiry()
	if err == nil && time.Now().Before(expiresAt) {
		s := Session{
			Token:     tok,
			ExpiresAt: expiresAt,
		}
		m.mu.Lock()
		m.session = &s
		m.ttl = ttl
		m.mu.Unlock()
		return s, true, nil
	}

	// A missing token file is the normal first-run path; staying silent is
	// correct there. But any other read error (corrupt/old-format/unreadable
	// file) means an existing token is being silently rotated, which
	// invalidates every deployed remote shim. Surface that so it is observable.
	if err != nil && !os.IsNotExist(err) {
		warnf("token: existing token file unreadable (%v); rotating token — all remote shims must be re-synced", err)
	}

	// Expired, missing, or unreadable — generate fresh token with the given TTL.
	m.ttl = ttl
	s, genErr := m.Generate()
	if genErr != nil {
		return Session{}, false, genErr
	}
	return s, false, nil
}

func (m *Manager) Validate(tok string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil {
		return ErrTokenInvalid
	}
	if time.Now().After(m.session.ExpiresAt) {
		return ErrTokenExpired
	}
	if !constantTimeTokenEqual(m.session.Token, strings.TrimSpace(tok)) {
		return ErrTokenInvalid
	}

	// Sliding expiration: extend if remaining TTL < half of total TTL.
	remaining := time.Until(m.session.ExpiresAt)
	if m.ttl > 0 && remaining < m.ttl/2 {
		m.session.ExpiresAt = time.Now().Add(m.ttl)
		// Best-effort persist; failure here is non-fatal.
		_, _ = WriteTokenFile(m.session.Token, m.session.ExpiresAt)
	}

	return nil
}

func constantTimeTokenEqual(expected, candidate string) bool {
	if len(expected) != len(candidate) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(candidate)) == 1
}

func (m *Manager) Current() *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.session == nil {
		return nil
	}
	cp := *m.session
	return &cp
}

// TokenDirOverride allows overriding the token directory (for testing).
// When empty, the default ~/.cache/cc-clip is used.
var TokenDirOverride string

func TokenDir() (string, error) {
	if TokenDirOverride != "" {
		return TokenDirOverride, os.MkdirAll(TokenDirOverride, 0700)
	}
	if env := os.Getenv("CC_CLIP_TOKEN_DIR"); env != "" {
		return env, os.MkdirAll(env, 0700)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cache", "cc-clip")
	return dir, os.MkdirAll(dir, 0700)
}

// WriteTokenFile writes a two-line token file: line 1 = token, line 2 = ISO8601 expiry.
//
// The write is atomic: content is written to a temp file in the same directory,
// fsync'd, then renamed onto session.token. A crash mid-write therefore leaves
// the previous (valid) token file intact rather than a truncated file that
// ReadTokenFileWithExpiry would mis-classify as old-format and silently rotate.
func WriteTokenFile(tok string, expiresAt time.Time) (string, error) {
	dir, err := TokenDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "session.token")
	content := tok + "\n" + expiresAt.Format(time.RFC3339) + "\n"

	// Temp file in the same directory so os.Rename is atomic (same filesystem).
	tmp, err := os.CreateTemp(dir, "session.token.tmp-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp token file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if we fail before the rename succeeds.
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("failed to chmod temp token file: %w", err)
	}
	if _, err := tmp.Write([]byte(content)); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("failed to write temp token file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("failed to fsync temp token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp token file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("failed to rename token file into place: %w", err)
	}
	return path, nil
}

// ReadTokenFile reads the token string from the token file.
// It supports both the new two-line format and the old single-line format.
// For backward compatibility, only the token string is returned.
func ReadTokenFile() (string, error) {
	dir, err := TokenDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "session.token")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("token file is empty")
	}
	return strings.TrimSpace(lines[0]), nil
}

// ReadTokenFileWithExpiry reads both the token string and expiry from the token file.
// If the file uses the old single-line format (no expiry line), an error is returned
// so the caller treats it as expired and generates a new token.
func ReadTokenFileWithExpiry() (string, time.Time, error) {
	dir, err := TokenDir()
	if err != nil {
		return "", time.Time{}, err
	}
	path := filepath.Join(dir, "session.token")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, err
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) < 2 {
		// Old format (single line) — treat as expired so a new token is generated.
		return "", time.Time{}, fmt.Errorf("token file missing expiry (old format)")
	}
	tok := strings.TrimSpace(lines[0])
	if tok == "" {
		return "", time.Time{}, fmt.Errorf("token file has empty token")
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(lines[1]))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid expiry timestamp: %w", err)
	}
	return tok, expiresAt, nil
}
