package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

const (
	maxImageSize    = 20 * 1024 * 1024 // 20MB
	maxNotifyBody   = 64 * 1024        // 64KB
	userAgent       = "cc-clip"
	criticalChCap   = 4
	claudeHookCType = "application/x-claude-hook"
)

const notifyChCap = 8

type Server struct {
	clipboard    ClipboardReader
	tokens       *token.Manager
	sessions     *session.Store
	dedup        *Deduper
	notifyCh     chan NotifyEnvelope
	criticalCh   chan NotifyEnvelope
	notifyNonces map[string]struct{}
	noncesMu     sync.RWMutex
	addr         string
	mux          *http.ServeMux
}

func NewServer(addr string, clipboard ClipboardReader, tokens *token.Manager, sessions *session.Store) *Server {
	s := &Server{
		clipboard:    clipboard,
		tokens:       tokens,
		sessions:     sessions,
		dedup:        NewDeduper(12 * time.Second),
		notifyCh:     make(chan NotifyEnvelope, notifyChCap),
		criticalCh:   make(chan NotifyEnvelope, criticalChCap),
		notifyNonces: make(map[string]struct{}),
		addr:         addr,
		mux:          http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /clipboard/type", s.authMiddleware(s.handleClipboardType))
	s.mux.HandleFunc("GET /clipboard/image", s.authMiddleware(s.handleClipboardImage))
	s.mux.HandleFunc("POST /notify", s.handleNotify)
	s.mux.HandleFunc("POST /register-nonce", s.authMiddleware(s.handleRegisterNonce))
	return s
}

// RegisterNotificationNonce adds a nonce to the dedicated notification
// auth registry. Notification nonces are separate from clipboard bearer
// tokens to enforce distinct auth domains. Returns an error if the nonce
// is empty or collides with a valid clipboard token.
func (s *Server) RegisterNotificationNonce(nonce string) error {
	if nonce == "" {
		return fmt.Errorf("empty nonce is not allowed")
	}
	if s.tokens.Validate(nonce) == nil {
		return fmt.Errorf("refusing to register clipboard token as notification nonce")
	}
	s.noncesMu.Lock()
	defer s.noncesMu.Unlock()
	s.notifyNonces[nonce] = struct{}{}
	return nil
}

// validNotificationNonce checks whether the given nonce is registered.
func (s *Server) validNotificationNonce(nonce string) bool {
	s.noncesMu.RLock()
	defer s.noncesMu.RUnlock()
	_, ok := s.notifyNonces[nonce]
	return ok
}

// enqueueEnvelope deduplicates then routes a notification envelope to
// the appropriate channel. Repeated non-critical notifications within
// the dedup window are suppressed. Critical envelopes (permission_prompt)
// bypass dedup and use criticalCh with a 500ms timeout. Non-critical
// envelopes use a select-default send to notifyCh, dropping on full.
func (s *Server) enqueueEnvelope(env NotifyEnvelope) {
	if allowed, _ := s.dedup.AllowAt(env, time.Now()); !allowed {
		return
	}
	if isAlwaysCritical(env) {
		select {
		case s.criticalCh <- env:
		case <-time.After(500 * time.Millisecond):
			log.Printf("WARN: criticalCh full, dropping critical envelope kind=%s", env.Kind)
		}
		return
	}
	select {
	case s.notifyCh <- env:
	default:
		// channel full: drop non-critical notification
	}
}

// RunNotifier consumes transfer events from both criticalCh (priority)
// and notifyCh, then delivers notifications via the Notifier interface.
// It blocks until ctx is cancelled. Panics in Notify/Deliver are recovered
// to prevent notification failures from crashing the daemon.
//
// If the supplied notifier also supports envelope delivery, envelopes are
// delivered directly so generic/tool notifications keep their structured
// payload. Legacy notifiers continue to receive bridged NotifyEvent values.
func (s *Server) RunNotifier(ctx context.Context, n Notifier) {
	for {
		var env NotifyEnvelope

		// Give critical notifications strict priority when both queues are ready.
		select {
		case <-ctx.Done():
			return
		case env = <-s.criticalCh:
		default:
			select {
			case env = <-s.criticalCh:
			case env = <-s.notifyCh:
			case <-ctx.Done():
				return
			}
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("notification panic recovered: %v", r)
				}
			}()

			if deliverer, ok := n.(interface {
				Deliver(context.Context, NotifyEnvelope) error
			}); ok {
				if err := deliverer.Deliver(ctx, env); err != nil {
					log.Printf("notification failed: %v", err)
				}
				return
			}

			evt := envelopeToEvent(env)
			if err := n.Notify(ctx, evt); err != nil {
				log.Printf("notification failed: %v", err)
			}
		}()
	}
}

// envelopeToEvent bridges a NotifyEnvelope back to a NotifyEvent for
// backward compatibility with the Notifier interface.
func envelopeToEvent(env NotifyEnvelope) NotifyEvent {
	if env.ImageTransfer != nil {
		return NotifyEvent{
			SessionID:   env.ImageTransfer.SessionID,
			Seq:         env.ImageTransfer.Seq,
			Fingerprint: env.ImageTransfer.Fingerprint,
			ImageData:   env.ImageTransfer.ImageData,
			Format:      env.ImageTransfer.Format,
			Width:       env.ImageTransfer.Width,
			Height:      env.ImageTransfer.Height,
			DuplicateOf: env.ImageTransfer.DuplicateOf,
		}
	}
	// For non-image envelopes, construct a synthetic event with
	// available metadata so existing Notifier implementations can
	// display something useful.
	return NotifyEvent{
		Format: string(env.Kind),
	}
}

// Handler returns the HTTP handler for this server.
// Useful for testing with httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Serve accepts connections on the given listener and serves HTTP.
func (s *Server) Serve(ln net.Listener) error {
	return http.Serve(ln, s.mux)
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}

	host, _, _ := net.SplitHostPort(s.addr)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		listener.Close()
		return fmt.Errorf("refusing to listen on non-loopback address: %s", host)
	}

	log.Printf("cc-clip daemon listening on %s", s.addr)
	return http.Serve(listener, s.mux)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check User-Agent
		ua := r.Header.Get("User-Agent")
		if ua != "" && !strings.HasPrefix(ua, userAgent) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		tok := strings.TrimPrefix(auth, "Bearer ")

		if err := s.tokens.Validate(tok); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleRegisterNonce accepts a notification nonce from an authenticated
// connect session. The request body is a JSON object with a "nonce" field.
// Protected by authMiddleware (requires clipboard bearer token).
func (s *Server) handleRegisterNonce(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.RegisterNotificationNonce(req.Nonce); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClipboardType(w http.ResponseWriter, r *http.Request) {
	info, err := s.clipboard.Type()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleClipboardImage(w http.ResponseWriter, r *http.Request) {
	info, err := s.clipboard.Type()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.Type != ClipboardImage {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	data, err := s.clipboard.ImageBytes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(data) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if len(data) > maxImageSize {
		http.Error(w, "image exceeds 20MB limit", http.StatusRequestEntityTooLarge)
		return
	}

	contentType := "image/png"
	if info.Format == "jpeg" {
		contentType = "image/jpeg"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, writeErr := w.Write(data)

	// Only notify if the image was successfully written to the client.
	// If the client disconnected mid-transfer, skip notification to avoid
	// false-positive "image transferred" confirmations.
	if writeErr != nil {
		return
	}

	sessionID := r.Header.Get("X-CC-Clip-Session")
	if sessionID != "" && s.sessions != nil {
		hash := sha256.Sum256(data)
		fingerprint := hex.EncodeToString(hash[:8])

		width, height := decodeImageDimensions(data)

		evt := s.sessions.AnalyzeAndRecord(sessionID, fingerprint, width, height, info.Format)
		env := newImageTransferEnvelope("clipboard", ImageTransferPayload{
			SessionID:   evt.SessionID,
			Seq:         evt.Seq,
			Fingerprint: evt.Fingerprint,
			ImageData:   data,
			Format:      info.Format,
			Width:       width,
			Height:      height,
			DuplicateOf: evt.DuplicateOf,
		})
		s.enqueueEnvelope(env)
	}
}

// handleNotify accepts notification payloads from remote hook scripts
// or generic senders. Auth is via notification nonce (separate from
// clipboard bearer token).
func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	nonce := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if nonce == "" || !s.validNotificationNonce(nonce) {
		http.Error(w, "invalid notification nonce", http.StatusUnauthorized)
		return
	}

	env, err := s.parseNotifyRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.enqueueEnvelope(env)
	w.WriteHeader(http.StatusNoContent)
}

// parseNotifyRequest decodes the request body into a NotifyEnvelope.
// Two content types are supported:
//   - application/x-claude-hook: Claude hook JSON, processed via ClassifyHookPayload
//   - anything else (typically application/json): generic JSON notification
func (s *Server) parseNotifyRequest(r *http.Request) (NotifyEnvelope, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxNotifyBody))
	if err != nil {
		return NotifyEnvelope{}, fmt.Errorf("failed to read body: %w", err)
	}
	if len(body) == 0 {
		return NotifyEnvelope{}, fmt.Errorf("empty request body")
	}

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, claudeHookCType) {
		return s.parseClaudeHookPayload(body)
	}
	return s.parseGenericJSON(body)
}

// parseClaudeHookPayload decodes Claude hook JSON and classifies it.
func (s *Server) parseClaudeHookPayload(body []byte) (NotifyEnvelope, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return NotifyEnvelope{}, fmt.Errorf("invalid JSON: %w", err)
	}

	hookType := "notification"
	if ht, ok := raw["hook_event_name"].(string); ok {
		hookType = strings.ToLower(ht)
	}

	env := ClassifyHookPayload(hookType, raw)
	if env == nil {
		return NotifyEnvelope{}, fmt.Errorf("classifier returned nil")
	}
	return *env, nil
}

// parseGenericJSON decodes a freeform JSON notification payload.
func (s *Server) parseGenericJSON(body []byte) (NotifyEnvelope, error) {
	var payload struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Urgency int    `json:"urgency"`
		Host    string `json:"host"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return NotifyEnvelope{}, fmt.Errorf("invalid JSON: %w", err)
	}

	return NotifyEnvelope{
		Kind:      KindGenericMessage,
		Source:    "generic",
		Host:      payload.Host,
		Timestamp: time.Now().UTC(),
		GenericMessage: &GenericMessagePayload{
			Title:    payload.Title,
			Body:     payload.Body,
			Urgency:  payload.Urgency,
			Verified: false,
		},
	}, nil
}

// decodeImageDimensions reads width and height from image data.
// Returns 0, 0 if decoding fails.
func decodeImageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// NotifyChannel exposes the non-critical notification queue to tests.
func (s *Server) NotifyChannel() <-chan NotifyEnvelope {
	return s.notifyCh
}
