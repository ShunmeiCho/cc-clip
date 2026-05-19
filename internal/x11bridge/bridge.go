package x11bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

const (
	// defaultMaxDirectSize is the max image size for direct (non-INCR) transfer.
	// X11's default max request size is ~256KB. We use a conservative limit.
	defaultMaxDirectSize = 256 * 1024

	// defaultChunkSize is the INCR chunk size (64KB is a safe, common choice).
	defaultChunkSize = 64 * 1024

	changePropertyRequestOverheadBytes = 24

	// httpTimeout is the timeout for HTTP requests to the cc-clip daemon.
	httpTimeout = 5 * time.Second
)

// Bridge is an X11 clipboard selection owner that serves image data
// fetched on-demand from a cc-clip HTTP daemon via SSH tunnel.
type Bridge struct {
	display   string
	port      int
	tokenFile string

	conn      *xgb.Conn
	screen    *xproto.ScreenInfo
	window    xproto.Window
	atoms     *AtomCache
	timestamp xproto.Timestamp

	activeIncr *IncrTransfer
	httpClient *http.Client
}

// clipboardTypeResponse mirrors daemon.ClipboardInfo.
type clipboardTypeResponse struct {
	Type   string `json:"type"`
	Format string `json:"format,omitempty"`
}

// New creates a Bridge connected to the given X display.
func New(display string, port int, tokenFile string) (*Bridge, error) {
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to X display %s: %w", display, err)
	}

	setup := xproto.Setup(conn)
	if len(setup.Roots) == 0 {
		conn.Close()
		return nil, fmt.Errorf("X display %s has no screens", display)
	}
	screen := setup.Roots[0]

	b := &Bridge{
		display:   display,
		port:      port,
		tokenFile: tokenFile,
		conn:      conn,
		screen:    &screen,
		atoms:     NewAtomCache(conn),
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}

	// Create an invisible window for selection ownership.
	wid, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot allocate window ID: %w", err)
	}
	b.window = wid

	err = xproto.CreateWindowChecked(
		conn,
		screen.RootDepth,
		wid,
		screen.Root,
		0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		0, nil,
	).Check()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot create window: %w", err)
	}

	// Preload all atoms to catch errors early.
	if err := b.atoms.Preload(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("atom initialization failed: %w", err)
	}

	// Claim CLIPBOARD selection ownership.
	if err := b.claimOwnership(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot claim CLIPBOARD ownership: %w", err)
	}

	return b, nil
}

// Run enters the X11 event loop. It blocks until the context is cancelled
// or an unrecoverable error occurs.
func (b *Bridge) Run(ctx context.Context) error {
	log.Printf("x11-bridge: running on DISPLAY=%s, port=%d", b.display, b.port)

	// Channel for X11 events.
	events := make(chan xgb.Event, 16)
	errors := make(chan xgb.Error, 16)

	go func() {
		defer close(events)
		defer close(errors)
		for {
			ev, err := b.conn.WaitForEvent()
			if !publishXEvent(ctx, events, errors, ev, err) {
				return
			}
		}
	}()

	clipboardAtom := b.atoms.MustGet(AtomNameClipboard)
	targetsAtom := b.atoms.MustGet(AtomNameTargets)
	timestampAtom, _ := b.atoms.Get(AtomNameTimestamp)
	imagePNGAtom := b.atoms.MustGet(AtomNameImagePNG)

	for {
		select {
		case <-ctx.Done():
			b.conn.Close()
			return ctx.Err()

		case ev, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("X11 connection closed")
			}

			switch e := ev.(type) {
			case xproto.SelectionRequestEvent:
				if e.Selection != clipboardAtom {
					refuseRequest(b.conn, e)
					continue
				}

				// Reject new requests if INCR is in progress.
				if b.activeIncr != nil && e.Target != targetsAtom {
					log.Printf("x11-bridge: rejecting request during active INCR transfer")
					refuseRequest(b.conn, e)
					continue
				}

				switch e.Target {
				case targetsAtom:
					b.handleTargets(e)
				case timestampAtom:
					b.handleTimestamp(e)
				case imagePNGAtom:
					b.handleImage(e)
				default:
					refuseRequest(b.conn, e)
				}

			case xproto.SelectionClearEvent:
				if e.Selection == clipboardAtom {
					log.Printf("x11-bridge: lost CLIPBOARD ownership, reclaiming")
					if err := b.claimOwnership(); err != nil {
						return fmt.Errorf("cannot reclaim CLIPBOARD: %w", err)
					}
				}

			case xproto.PropertyNotifyEvent:
				if b.activeIncr != nil &&
					e.Atom == b.activeIncr.Property &&
					e.Window == b.activeIncr.Requestor &&
					e.State == xproto.PropertyDelete {
					b.handleIncrChunk()
				}
			}

		case xerr, ok := <-errors:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("X11 connection closed")
			}
			// Log X11 protocol errors but continue running.
			log.Printf("x11-bridge: X11 error: %v", xerr)
		}
	}
}

// claimOwnership sets this bridge as the CLIPBOARD selection owner.
func (b *Bridge) claimOwnership() error {
	clipboardAtom := b.atoms.MustGet(AtomNameClipboard)

	xproto.SetSelectionOwner(
		b.conn,
		b.window,
		clipboardAtom,
		xproto.TimeCurrentTime,
	)

	// Verify we actually got ownership.
	reply, err := xproto.GetSelectionOwner(b.conn, clipboardAtom).Reply()
	if err != nil {
		return fmt.Errorf("GetSelectionOwner failed: %w", err)
	}
	if reply.Owner != b.window {
		return fmt.Errorf("failed to acquire CLIPBOARD ownership (owner: %d, expected: %d)", reply.Owner, b.window)
	}

	b.timestamp = xproto.TimeCurrentTime
	log.Printf("x11-bridge: claimed CLIPBOARD ownership")
	return nil
}

func publishXEvent(ctx context.Context, events chan<- xgb.Event, errors chan<- xgb.Error, ev xgb.Event, xerr xgb.Error) bool {
	if ev == nil && xerr == nil {
		return false
	}
	if ev != nil {
		select {
		case events <- ev:
		case <-ctx.Done():
			return false
		}
	}
	if xerr != nil {
		select {
		case errors <- xerr:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// handleTargets responds to a TARGETS request.
func (b *Bridge) handleTargets(event xproto.SelectionRequestEvent) {
	hasImage := false
	info, err := b.fetchClipboardType()
	if err != nil {
		log.Printf("x11-bridge: clipboard type check failed: %v", err)
	} else {
		hasImage = info.Type == "image"
	}

	if err := handleTargetsRequest(b.conn, event, b.atoms, hasImage); err != nil {
		log.Printf("x11-bridge: TARGETS response failed: %v", err)
	}
}

// handleTimestamp responds to a TIMESTAMP request.
func (b *Bridge) handleTimestamp(event xproto.SelectionRequestEvent) {
	if err := handleTimestampRequest(b.conn, event, b.timestamp); err != nil {
		log.Printf("x11-bridge: TIMESTAMP response failed: %v", err)
	}
}

// handleImage responds to an image/png request.
func (b *Bridge) handleImage(event xproto.SelectionRequestEvent) {
	imageData, err := b.fetchClipboardImage()
	if err != nil {
		log.Printf("x11-bridge: image fetch failed: %v", err)
		refuseRequest(b.conn, event)
		return
	}

	// Determine max direct transfer size from X server's max request length.
	maxDirect := maxDirectSizeFromRequestLength(xproto.Setup(b.conn).MaximumRequestLength)

	incrData, err := handleImageRequest(b.conn, event, b.atoms, imageData, maxDirect)
	if err != nil {
		log.Printf("x11-bridge: image response failed: %v", err)
		return
	}

	if incrData != nil {
		// Start INCR transfer.
		chunkSize := defaultChunkSize
		transfer, err := startIncr(b.conn, event, b.atoms, incrData, chunkSize)
		if err != nil {
			log.Printf("x11-bridge: INCR start failed: %v", err)
			refuseRequest(b.conn, event)
			return
		}
		b.activeIncr = transfer
		log.Printf("x11-bridge: started INCR transfer (%d bytes)", len(incrData))
	}
}

func maxDirectSizeFromRequestLength(maxReq uint16) int {
	if maxReq == 0 {
		return defaultMaxDirectSize
	}
	n := int(maxReq)*4 - changePropertyRequestOverheadBytes
	if n < 0 {
		return 0
	}
	return n
}

// handleIncrChunk writes the next INCR chunk on PropertyDelete.
func (b *Bridge) handleIncrChunk() {
	if b.activeIncr == nil {
		return
	}

	if err := writeNextChunk(b.conn, b.activeIncr); err != nil {
		log.Printf("x11-bridge: INCR chunk write failed: %v", err)
		b.activeIncr = nil
		return
	}

	if b.activeIncr.IsComplete() {
		log.Printf("x11-bridge: INCR transfer complete")
		b.activeIncr = nil
	}
}

// readToken reads the session token from the token file.
func (b *Bridge) readToken() (string, error) {
	data, err := os.ReadFile(b.tokenFile)
	if err != nil {
		return "", fmt.Errorf("token read failed: %w", err)
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", fmt.Errorf("token file is empty")
	}
	// Token file may contain "token\nexpires_at" — take only the first line.
	if i := strings.IndexByte(tok, '\n'); i >= 0 {
		tok = tok[:i]
	}
	return tok, nil
}

// readSessionID reads the session ID from the session.id file next to the token file.
// Returns empty string (not error) if file doesn't exist, since session tracking is optional.
func (b *Bridge) readSessionID() string {
	dir := filepath.Dir(b.tokenFile)
	data, err := os.ReadFile(filepath.Join(dir, "session.id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// fetchClipboardType queries the cc-clip daemon for clipboard content type.
func (b *Bridge) fetchClipboardType() (*clipboardTypeResponse, error) {
	tok, err := b.readToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/clipboard/type", b.port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	if sid := b.readSessionID(); sid != "" {
		req.Header.Set("X-CC-Clip-Session", sid)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tunnel unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("token invalid (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var info clipboardTypeResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	return &info, nil
}

// fetchClipboardImage downloads the clipboard image from the cc-clip daemon.
func (b *Bridge) fetchClipboardImage() ([]byte, error) {
	tok, err := b.readToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/clipboard/image", b.port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "cc-clip/0.1")
	if sid := b.readSessionID(); sid != "" {
		req.Header.Set("X-CC-Clip-Session", sid)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tunnel unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("token invalid (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, fmt.Errorf("clipboard no image")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("image download failed: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("image fetch returned empty")
	}

	return data, nil
}
