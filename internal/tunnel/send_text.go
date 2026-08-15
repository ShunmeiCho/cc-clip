package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// ErrDaemonUnreachable wraps transport-level SendText failures so callers can
// map them to the TunnelUnreachable business exit code without string matching.
var ErrDaemonUnreachable = errors.New("daemon unreachable through the tunnel")

const defaultMaxSendTextMB = 1

// MaxSendTextSize is the client-side cap for SendText bodies, mirroring the
// daemon's CC_CLIP_MAX_TEXT_MB so an oversized copy fails on the remote with
// a clear message instead of burning tunnel bandwidth to earn a 413.
func MaxSendTextSize() int {
	if env := os.Getenv("CC_CLIP_MAX_TEXT_MB"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			return v * 1024 * 1024
		}
	}
	return defaultMaxSendTextMB * 1024 * 1024
}

// SendText places text on the LOCAL machine's clipboard through the tunnel
// (POST /clipboard/text, #128). The body is sent verbatim — no newline
// normalization — because bypassing the terminal's soft-wrap mangling is the
// reason the endpoint exists.
func (c *Client) SendText(text string) error {
	req, err := http.NewRequest("POST", c.baseURL+"/clipboard/text", strings.NewReader(text))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		return ErrTokenInvalid
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("text exceeds the daemon's size limit (raise CC_CLIP_MAX_TEXT_MB on the local machine)")
	case http.StatusMethodNotAllowed, http.StatusNotFound, http.StatusNotImplemented:
		// 405/404: an older daemon that predates the write endpoint answers
		// from its GET-only mux. 501: a daemon built without a platform
		// writer. All three fix the same way — on the LOCAL side.
		return fmt.Errorf("the daemon does not support clipboard write; upgrade cc-clip on the local machine and restart it (cc-clip update)")
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("clipboard write failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
}
