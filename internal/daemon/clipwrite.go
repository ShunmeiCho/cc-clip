package daemon

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// ClipboardTextWriter writes text to the OS clipboard. The reverse of
// ClipboardReader: it backs POST /clipboard/text, which lets the REMOTE side
// of the tunnel put bytes on the LOCAL clipboard (`cc-clip copy`, #128) so
// copied text never passes through terminal rendering and its soft-wrap
// newlines.
type ClipboardTextWriter interface {
	WriteText(text string) error
}

// SetTextWriter enables the clipboard-write endpoint. Without a writer the
// endpoint answers 501, so a platform with no writer implementation degrades
// loudly instead of pretending success.
func (s *Server) SetTextWriter(w ClipboardTextWriter) {
	s.textWriter = w
}

// handleClipboardWrite accepts text from an authenticated remote and places it
// on the local clipboard verbatim. Newlines are NOT normalized: preserving the
// exact bytes is the point (#128) — the terminal's soft-wrap mangling is what
// this endpoint exists to bypass.
//
// Every accepted write emits a calm notification. A remote that can write the
// local clipboard is a paste-hijack primitive; the notification makes each
// write visible so poisoning cannot happen silently. The clipboard CONTENT is
// deliberately absent from the notification — it may be a secret, and
// notification text reaches tmux status lines and notification centers.
func (s *Server) handleClipboardWrite(w http.ResponseWriter, r *http.Request) {
	if s.textWriter == nil {
		http.Error(w, "clipboard write not supported by this daemon", http.StatusNotImplemented)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, int64(maxTextSize())+1))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	if len(body) > maxTextSize() {
		http.Error(w, fmt.Sprintf("text exceeds %dMB limit", maxTextMB()), http.StatusRequestEntityTooLarge)
		return
	}

	if err := s.textWriter.WriteText(string(body)); err != nil {
		http.Error(w, fmt.Sprintf("clipboard write failed: %v", err), http.StatusInternalServerError)
		return
	}

	s.enqueueEnvelope(NotifyEnvelope{
		Kind:      KindGenericMessage,
		Source:    "clipboard-write",
		Timestamp: time.Now().UTC(),
		GenericMessage: &GenericMessagePayload{
			Title: "Clipboard set by remote",
			// Deliberately STABLE text (no byte count): the dedup window keys
			// on the envelope fingerprint, so a constant body lets a burst of
			// writes — every neovim yank on the transparent shim path (#128
			// phase 2) is one write — collapse into a single notification
			// instead of spamming one per yank. Poisoning visibility is
			// preserved: at least one notification always fires per window.
			Body:    "A remote session wrote to this clipboard.",
			Urgency: 0,
			// The daemon authored this text itself; none of it came from the
			// request. Leaving Verified false made formatNotification prefix
			// the daemon's own message with "[unverified]", which is the one
			// marker a user has for "this text is not ours" — spending it on
			// our own notification is what makes it stop meaning anything.
			Verified: true,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}
