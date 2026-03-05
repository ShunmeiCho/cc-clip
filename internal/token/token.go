package token

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

func (m *Manager) Validate(token string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.session == nil {
		return ErrTokenInvalid
	}
	if time.Now().After(m.session.ExpiresAt) {
		return ErrTokenExpired
	}
	if m.session.Token != strings.TrimSpace(token) {
		return ErrTokenInvalid
	}
	return nil
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

func WriteTokenFile(tok string) (string, error) {
	dir, err := TokenDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "session.token")
	if err := os.WriteFile(path, []byte(tok+"\n"), 0600); err != nil {
		return "", err
	}
	return path, nil
}

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
	return strings.TrimSpace(string(data)), nil
}
