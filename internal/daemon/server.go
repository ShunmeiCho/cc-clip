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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

const (
	defaultMaxImageMB = 20
	defaultMaxTextMB  = 1
	maxNotifyBody     = 64 * 1024 // 64KB
	userAgent         = "cc-clip"
	criticalChCap     = 4
	claudeHookCType   = "application/x-claude-hook"
)

// sizeLimitMB reads a whole-megabyte clipboard cap override from env.
// Invalid or non-positive values fall back to the default, mirroring
// CC_CLIP_PORT handling.
func sizeLimitMB(envName string, defMB int) int {
	if env := os.Getenv(envName); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			return v
		}
	}
	return defMB
}

func maxImageMB() int { return sizeLimitMB("CC_CLIP_MAX_IMAGE_MB", defaultMaxImageMB) }

func maxImageSize() int { return maxImageMB() * 1024 * 1024 }

func maxTextMB() int { return sizeLimitMB("CC_CLIP_MAX_TEXT_MB", defaultMaxTextMB) }

func maxTextSize() int { return maxTextMB() * 1024 * 1024 }

const (
	httpReadHeaderTimeout = 2 * time.Second
	httpReadTimeout       = 10 * time.Second
	httpWriteTimeout      = 30 * time.Second
	httpIdleTimeout       = 60 * time.Second
)

const notifyChCap = 8

type Server struct {
	clipboard         ClipboardReader
	tokens            *token.Manager
	sessions          *session.Store
	dedup             *Deduper
	notifyCh          chan NotifyEnvelope
	criticalCh        chan NotifyEnvelope
	notifyNonces      map[string]nonceEntry
	notifyNoncesOrder []string // insertion order for FIFO eviction when cap is exceeded
	noncesMu          sync.RWMutex
	persistNonces     bool
	addr              string
	mux               *http.ServeMux
}

func NewServer(addr string, clipboard ClipboardReader, tokens *token.Manager, sessions *session.Store) *Server {
	s := &Server{
		clipboard:         clipboard,
		tokens:            tokens,
		sessions:          sessions,
		dedup:             NewDeduper(12 * time.Second),
		notifyCh:          make(chan NotifyEnvelope, notifyChCap),
		criticalCh:        make(chan NotifyEnvelope, criticalChCap),
		notifyNonces:      make(map[string]nonceEntry),
		notifyNoncesOrder: make([]string, 0, 32),
		addr:              addr,
		mux:               http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /clipboard/type", s.authMiddleware(s.handleClipboardType))
	s.mux.HandleFunc("GET /clipboard/image", s.authMiddleware(s.handleClipboardImage))
	s.mux.HandleFunc("GET /clipboard/text", s.authMiddleware(s.handleClipboardText))
	s.mux.HandleFunc("POST /notify", s.handleNotify)
	s.mux.HandleFunc("POST /register-nonce", s.authMiddleware(s.handleRegisterNonce))
	return s
}

// EnableNoncePersistence makes notification nonce mutations durable across
// daemon restarts. Tests leave this disabled unless they are exercising the
// persistence contract directly.
func (s *Server) EnableNoncePersistence() {
	s.noncesMu.Lock()
	s.persistNonces = true
	s.noncesMu.Unlock()
}

// nonceEntry tracks metadata for a registered notification nonce.
type nonceEntry struct {
	Host      string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// nonceTTL is the default lifetime for notification nonces.
const nonceTTL = 7 * 24 * time.Hour // 7 days

// maxNonces caps the in-memory notification nonce registry so a
// long-running daemon receiving many cross-host re-registrations
// cannot accumulate unbounded memory. Eviction is FIFO by insertion
// order — the oldest registered nonce is dropped first.
const maxNonces = 1024

// RegisterNotificationNonce adds a nonce to the dedicated notification
// auth registry. Notification nonces are separate from clipboard bearer
// tokens to enforce distinct auth domains. When a new nonce is registered
// for the same host, any previous nonce for that host is revoked.
// Returns an error if the nonce is empty or collides with a valid clipboard token.
func (s *Server) RegisterNotificationNonce(nonce string) error {
	return s.RegisterNotificationNonceForHost(nonce, "")
}

// RegisterNotificationNonceForHost registers a nonce bound to a specific host.
// Any previous nonce for the same host is automatically revoked. The registry
// is FIFO-capped at maxNonces; if adding this nonce would exceed the cap, the
// oldest entries (by insertion order) are evicted first.
func (s *Server) RegisterNotificationNonceForHost(nonce, host string) error {
	if nonce == "" {
		return fmt.Errorf("empty nonce is not allowed")
	}
	if s.tokens.Validate(nonce) == nil {
		return fmt.Errorf("refusing to register clipboard token as notification nonce")
	}
	now := time.Now()
	s.noncesMu.Lock()
	defer s.noncesMu.Unlock()
	prevNonces := cloneNonceMap(s.notifyNonces)
	prevOrder := append([]string(nil), s.notifyNoncesOrder...)
	// Revoke previous nonce for the same host. We must update both the
	// map AND the order slice so same-host re-registrations cannot grow
	// notifyNoncesOrder unbounded (which would defeat the entire cap).
	if host != "" {
		for k, v := range s.notifyNonces {
			if v.Host == host {
				delete(s.notifyNonces, k)
				s.removeFromNoncesOrder(k)
			}
		}
	}
	// Track insertion order for FIFO eviction. Only append if this is a
	// brand-new key; same-key re-registration keeps the original order
	// slot (so the entry doesn't artificially move to the back).
	if _, existed := s.notifyNonces[nonce]; !existed {
		s.notifyNoncesOrder = append(s.notifyNoncesOrder, nonce)
	}
	s.notifyNonces[nonce] = nonceEntry{
		Host:      host,
		IssuedAt:  now,
		ExpiresAt: now.Add(nonceTTL),
	}
	// Cap registry size by evicting oldest entries beyond the limit.
	for len(s.notifyNonces) > maxNonces && len(s.notifyNoncesOrder) > 0 {
		oldest := s.notifyNoncesOrder[0]
		s.notifyNoncesOrder = s.notifyNoncesOrder[1:]
		delete(s.notifyNonces, oldest)
	}
	if s.persistNonces {
		if err := s.persistNoncesLocked(); err != nil {
			s.notifyNonces = prevNonces
			s.notifyNoncesOrder = prevOrder
			return fmt.Errorf("persist notification nonce registry: %w", err)
		}
	}
	return nil
}

// removeFromNoncesOrder removes the first occurrence of nonce from
// notifyNoncesOrder. Linear O(n), but n is bounded by maxNonces.
// Caller must hold s.noncesMu.
func (s *Server) removeFromNoncesOrder(nonce string) {
	for i, n := range s.notifyNoncesOrder {
		if n == nonce {
			s.notifyNoncesOrder = append(s.notifyNoncesOrder[:i], s.notifyNoncesOrder[i+1:]...)
			return
		}
	}
}

// validNotificationNonce checks whether the given nonce is registered and not expired.
func (s *Server) validNotificationNonce(nonce string) bool {
	_, ok := s.lookupValidNonce(nonce)
	return ok
}

// lookupValidNonce returns the host bound to the nonce and whether the nonce
// is registered and unexpired. The returned host is authoritative: it is the
// SSH host that minted the nonce (set at registration), not anything supplied
// in the request body. Callers use it to attribute notifications by auth
// context instead of trusting spoofable body fields. The host is "" for
// nonces registered without a host binding.
func (s *Server) lookupValidNonce(nonce string) (host string, ok bool) {
	s.noncesMu.RLock()
	defer s.noncesMu.RUnlock()
	entry, found := s.notifyNonces[nonce]
	if !found {
		return "", false
	}
	if !time.Now().Before(entry.ExpiresAt) {
		return "", false
	}
	return entry.Host, true
}

// CleanupExpiredNonces removes nonces that have passed their TTL.
// Order slice is updated alongside so it does not grow unbounded.
func (s *Server) CleanupExpiredNonces() {
	now := time.Now()
	s.noncesMu.Lock()
	defer s.noncesMu.Unlock()
	for k, v := range s.notifyNonces {
		if now.After(v.ExpiresAt) {
			delete(s.notifyNonces, k)
			s.removeFromNoncesOrder(k)
		}
	}
	if s.persistNonces {
		if err := s.persistNoncesLocked(); err != nil {
			log.Printf("WARN: failed to persist notification nonce cleanup: %v", err)
		}
	}
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
		// Use an explicit timer so the success path can Stop() it instead of
		// leaving a parked timer to fire later (time.After leaks the timer
		// until it expires; staticcheck SA1015).
		t := time.NewTimer(500 * time.Millisecond)
		select {
		case s.criticalCh <- env:
			t.Stop()
		case <-t.C:
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
	if err := requireLoopbackListener(ln.Addr()); err != nil {
		_ = ln.Close()
		return err
	}
	return s.httpServer().Serve(ln)
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}

	if err := requireLoopbackListener(listener.Addr()); err != nil {
		_ = listener.Close()
		return err
	}

	log.Printf("cc-clip daemon listening on %s", s.addr)
	return s.httpServer().Serve(listener)
}

func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}
}

func requireLoopbackListener(addr net.Addr) error {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		if tcpAddr.IP != nil && tcpAddr.IP.IsLoopback() {
			return nil
		}
		host := "unspecified"
		if tcpAddr.IP != nil {
			host = tcpAddr.IP.String()
		}
		return fmt.Errorf("refusing to listen on non-loopback address: %s", host)
	}

	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return fmt.Errorf("refusing to listen on non-loopback address: %s", addr.String())
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to listen on non-loopback address: %s", host)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}

		ua := r.Header.Get("User-Agent")
		if !strings.HasPrefix(ua, userAgent) {
			http.Error(w, "forbidden", http.StatusForbidden)
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
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "cc-clip"})
}

// handleRegisterNonce accepts a notification nonce from an authenticated
// connect session. The request body is a JSON object with "nonce" and
// optional "host" fields. When host is non-empty, any previous nonce
// bound to that host is revoked so a reconnect immediately invalidates
// the prior credential instead of waiting for TTL or FIFO eviction.
// Protected by authMiddleware (requires clipboard bearer token).
func (s *Server) handleRegisterNonce(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Nonce string `json:"nonce"`
		Host  string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.RegisterNotificationNonceForHost(req.Nonce, req.Host); err != nil {
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

	if len(data) > maxImageSize() {
		http.Error(w, fmt.Sprintf("image exceeds %dMB limit", maxImageMB()), http.StatusRequestEntityTooLarge)
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

func (s *Server) handleClipboardText(w http.ResponseWriter, r *http.Request) {
	info, err := s.clipboard.Type()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.Type != ClipboardText {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	text, err := s.clipboard.Text()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if text == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(text) > maxTextSize() {
		http.Error(w, fmt.Sprintf("text exceeds %dMB limit", maxTextMB()), http.StatusRequestEntityTooLarge)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len([]byte(text))))
	_, _ = w.Write([]byte(text))
}

// handleNotify accepts notification payloads from remote hook scripts
// or generic senders. Auth is via notification nonce (separate from
// clipboard bearer token).
func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	nonce := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	boundHost, ok := "", false
	if nonce != "" {
		boundHost, ok = s.lookupValidNonce(nonce)
	}
	if !ok {
		http.Error(w, "invalid notification nonce", http.StatusUnauthorized)
		return
	}

	env, err := s.parseNotifyRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Host attribution comes from the auth context, never the request body.
	// The host bound to the nonce at registration is authoritative; a body
	// that claims a different host (env.Host from _cc_clip_host or the JSON
	// "host" field) is overridden so attribution cannot be spoofed. When the
	// nonce was registered without a host binding, the body host is kept.
	if boundHost != "" {
		env.Host = boundHost
	}

	s.enqueueEnvelope(env)
	w.WriteHeader(http.StatusNoContent)
}

// parseNotifyRequest decodes the request body into a NotifyEnvelope.
// Two content types are supported:
//   - application/x-claude-hook: Claude hook JSON, processed via ClassifyHookPayload
//   - anything else (typically application/json): generic JSON notification
func (s *Server) parseNotifyRequest(r *http.Request) (NotifyEnvelope, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxNotifyBody+1))
	if err != nil {
		return NotifyEnvelope{}, fmt.Errorf("failed to read body: %w", err)
	}
	if len(body) > maxNotifyBody {
		return NotifyEnvelope{}, fmt.Errorf("notification body exceeds 64KB limit")
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
		Sound   string `json:"sound"`
		Trusted bool   `json:"trusted"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return NotifyEnvelope{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if payload.Title == "" && payload.Body == "" {
		return NotifyEnvelope{}, fmt.Errorf("notification requires a non-empty title or body")
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
			Verified: payload.Trusted,
			Sound:    payload.Sound,
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
