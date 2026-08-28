package x11bridge

import (
	"fmt"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Well-known atom names used by the X11 clipboard selection protocol.
const (
	AtomNameClipboard = "CLIPBOARD"
	AtomNameTargets   = "TARGETS"
	AtomNameTimestamp = "TIMESTAMP"
	AtomNameMultiple  = "MULTIPLE"
	AtomNameIncr      = "INCR"
	AtomNameImagePNG  = "image/png"
)

// AtomCache provides lazy, cached interning of X11 atoms.
// All methods are safe for concurrent use.
type AtomCache struct {
	conn  *xgb.Conn
	mu    sync.Mutex
	cache map[string]xproto.Atom
}

// NewAtomCache creates an AtomCache for the given X11 connection.
func NewAtomCache(conn *xgb.Conn) *AtomCache {
	return &AtomCache{
		conn:  conn,
		cache: make(map[string]xproto.Atom),
	}
}

// Get returns the atom for the given name, interning it on first access.
func (a *AtomCache) Get(name string) (xproto.Atom, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if atom, ok := a.cache[name]; ok {
		return atom, nil
	}

	reply, err := xproto.InternAtom(a.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, fmt.Errorf("failed to intern atom %q: %w", name, err)
	}

	a.cache[name] = reply.Atom
	return reply.Atom, nil
}

// MustGet returns the atom for the given name, panicking on error.
// Use only during initialization where failure is unrecoverable.
func (a *AtomCache) MustGet(name string) xproto.Atom {
	atom, err := a.Get(name)
	if err != nil {
		panic(err)
	}
	return atom
}

// Preload interns all well-known atoms used by the bridge.
// Call this during initialization to catch errors early.
func (a *AtomCache) Preload() error {
	names := []string{
		AtomNameClipboard,
		AtomNameTargets,
		AtomNameTimestamp,
		AtomNameIncr,
		AtomNameImagePNG,
	}
	for _, name := range names {
		if _, err := a.Get(name); err != nil {
			return err
		}
	}
	return nil
}
