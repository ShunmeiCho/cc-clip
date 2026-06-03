// Package plugin provides a reusable notify-dispatch abstraction that forwards
// agent hook payloads to the local cc-clip daemon's /notify endpoint. It shares
// the exact HTTP-POST core with cmd/cc-clip's notify subcommand so deployed
// behavior is byte-identical.
package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

// PostNotification sends a generic notification to the local cc-clip daemon.
// It reads the notification nonce from ~/.cache/cc-clip/notify.nonce for auth.
//
// This is the shared core lifted verbatim from
// cmd/cc-clip/main.go:postGenericNotification. The wire bytes (field order,
// headers, status handling) are preserved so the deployed notify path is
// unchanged.
func PostNotification(port int, msg daemon.GenericMessagePayload) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	nonceFile := filepath.Join(home, ".cache", "cc-clip", "notify.nonce")
	nonceBytes, err := os.ReadFile(nonceFile)
	if err != nil {
		return fmt.Errorf("cannot read nonce file %s: %w", nonceFile, err)
	}
	nonce := strings.TrimSpace(string(nonceBytes))

	payload := struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Urgency int    `json:"urgency"`
		Sound   string `json:"sound,omitempty"`
		Trusted bool   `json:"trusted,omitempty"`
	}{
		Title:   msg.Title,
		Body:    msg.Body,
		Urgency: msg.Urgency,
		Sound:   msg.Sound,
		Trusted: msg.Verified,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/notify", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+nonce)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cc-clip-notify/0.1")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// postHookPayload forwards raw Claude hook JSON to the daemon's /notify endpoint
// using the same auth and headers as the cc-clip-hook bash script
// (internal/shim/hook_template.go lines 45-54): Authorization Bearer nonce,
// Content-Type application/x-claude-hook, User-Agent cc-clip-hook/0.1. The
// claude-hook content type tells the daemon to classify the body as a hook
// payload rather than a generic JSON notification.
func postHookPayload(port int, raw []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	nonceFile := filepath.Join(home, ".cache", "cc-clip", "notify.nonce")
	nonceBytes, err := os.ReadFile(nonceFile)
	if err != nil {
		return fmt.Errorf("cannot read nonce file %s: %w", nonceFile, err)
	}
	nonce := strings.TrimSpace(string(nonceBytes))

	url := fmt.Sprintf("http://127.0.0.1:%d/notify", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+nonce)
	req.Header.Set("Content-Type", "application/x-claude-hook")
	req.Header.Set("User-Agent", "cc-clip-hook/0.1")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send hook payload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// injectHost returns raw with the "_cc_clip_host" key set to the resolved host
// alias, reproducing the cc-clip-hook bash flow (hook_template.go:24-31). On any
// parse/marshal failure it returns raw unchanged, matching the bash
// `|| echo "$_payload"` fallback so malformed or non-object payloads still post.
func injectHost(raw []byte) []byte {
	var d map[string]interface{}
	if err := json.Unmarshal(raw, &d); err != nil || d == nil {
		return raw
	}
	d["_cc_clip_host"] = hostAlias()
	out, err := json.Marshal(d)
	if err != nil {
		return raw
	}
	return out
}

// hostAlias resolves the host label injected into hook payloads, matching the
// bash hook's ${CC_CLIP_HOST_ALIAS:-$(hostname -s)} precedence.
func hostAlias() string {
	if v := strings.TrimSpace(os.Getenv("CC_CLIP_HOST_ALIAS")); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil {
		// match `hostname -s`: short host, strip domain.
		if i := strings.IndexByte(h, '.'); i >= 0 {
			h = h[:i]
		}
		return h
	}
	return ""
}

// parseCodexNotifyPayload extracts a GenericMessagePayload from the Codex JSON
// format. Codex passes {"last-assistant-message": "..."} as its notify payload.
// The extracted message becomes the body with title "Codex".
//
// This is an exact copy of cmd/cc-clip/main.go:parseCodexNotifyPayload. It is
// duplicated rather than moved so that main_test.go's references to
// main.parseCodexNotifyPayload remain unchanged; TestCodexNotifyParseMatchesMainParser
// guards that the copies agree.
func parseCodexNotifyPayload(payload string) (daemon.GenericMessagePayload, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return daemon.GenericMessagePayload{}, fmt.Errorf("failed to parse JSON: %w", err)
	}

	lastMsg, _ := raw["last-assistant-message"].(string)

	return daemon.GenericMessagePayload{
		Title:    "Codex",
		Body:     lastMsg,
		Urgency:  1,
		Verified: true,
	}, nil
}

// parseAntigravityNotifyPayload extracts a GenericMessagePayload from an
// Antigravity (agy) Stop hook payload. Unlike Codex, the Antigravity Stop hook
// stdin carries {"terminationReason": "...", "fullyIdle": bool, "error": "..."}
// (no last-assistant-message), so it has its own parser and the title
// "Antigravity"; the body is composed from those fields and is never empty.
func parseAntigravityNotifyPayload(payload string) (daemon.GenericMessagePayload, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return daemon.GenericMessagePayload{}, fmt.Errorf("failed to parse JSON: %w", err)
	}
	return daemon.GenericMessagePayload{
		Title:    "Antigravity",
		Body:     antigravityStopBody(raw),
		Urgency:  1,
		Verified: true,
	}, nil
}

// parseOpencodeNotifyPayload maps the JS plugin's {"event": {...}} envelope to a
// GenericMessagePayload. The JS plugin sends JSON.stringify({ event }), so the
// opencode event object is nested under "event". The title is "opencode"; the
// body is derived from the event type via opencodeBody. Like codex/agy, it sets
// Verified=true (the daemon Trusted flag) because the source is a trusted plugin.
func parseOpencodeNotifyPayload(payload string) (daemon.GenericMessagePayload, error) {
	var raw struct {
		Event struct {
			Type       string                 `json:"type"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"event"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return daemon.GenericMessagePayload{}, fmt.Errorf("failed to parse JSON: %w", err)
	}
	return daemon.GenericMessagePayload{
		Title:    "opencode",
		Body:     opencodeBody(raw.Event.Type),
		Urgency:  1,
		Verified: true,
	}, nil
}

// opencodeBody maps an opencode event type to a human-readable body. Only
// session.idle (turn-complete) has a friendly string; any other type echoes the
// type so unmapped events still produce a non-misleading body.
func opencodeBody(eventType string) string {
	switch eventType {
	case "session.idle":
		return "Session idle - awaiting input"
	default:
		return eventType
	}
}

// antigravityStopBody composes a human-readable body from the Antigravity Stop
// payload, preferring an error, then the termination reason, with the idle
// state as a fallback so the notification body is never empty.
func antigravityStopBody(raw map[string]interface{}) string {
	if e, _ := raw["error"].(string); strings.TrimSpace(e) != "" {
		return "Error: " + e
	}
	if r, _ := raw["terminationReason"].(string); strings.TrimSpace(r) != "" {
		return r
	}
	if idle, _ := raw["fullyIdle"].(bool); idle {
		return "Idle"
	}
	return "Stopped"
}
