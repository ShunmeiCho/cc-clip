package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/shunmei/cc-clip/internal/token"
)

const (
	maxImageSize = 20 * 1024 * 1024 // 20MB
	userAgent    = "cc-clip"
)

type Server struct {
	clipboard ClipboardReader
	tokens    *token.Manager
	addr      string
	mux       *http.ServeMux
}

func NewServer(addr string, clipboard ClipboardReader, tokens *token.Manager) *Server {
	s := &Server{
		clipboard: clipboard,
		tokens:    tokens,
		addr:      addr,
		mux:       http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /clipboard/type", s.authMiddleware(s.handleClipboardType))
	s.mux.HandleFunc("GET /clipboard/image", s.authMiddleware(s.handleClipboardImage))
	return s
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}

	host, _, _ := net.SplitHostPort(s.addr)
	if !isAllowedAddress(host) {
		listener.Close()
		return fmt.Errorf("refusing to listen on non-private address: %s (only loopback and private/CGNAT ranges are allowed)", host)
	}

	log.Printf("cc-clip daemon listening on %s", s.addr)
	return http.Serve(listener, s.mux)
}

// isAllowedAddress returns true if the host is a loopback, private (RFC 1918),
// or CGNAT (100.64.0.0/10, used by Tailscale) address. Binding to 0.0.0.0 or
// :: is also allowed as it includes loopback; the bearer token protects against
// unauthorized access.
func isAllowedAddress(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	// RFC 1918 + CGNAT (100.64.0.0/10, covers Tailscale 100.x.x.x)
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"fd00::/8",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
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
	w.Write(data)
}
