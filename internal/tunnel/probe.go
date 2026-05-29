package tunnel

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrDaemonNotAnswering indicates the address accepted a TCP connection but the
// cc-clip daemon did not answer a /health request. This distinguishes a live
// SSH RemoteForward listener whose local daemon is down from a fully
// unreachable address.
var ErrDaemonNotAnswering = errors.New("tunnel reachable but daemon not answering")

// Probe verifies TCP reachability of addr. Note: an SSH RemoteForward listener
// satisfies the handshake even when the local daemon is down. Use ProbeHealth
// when daemon liveness (not just port reachability) must be confirmed.
func Probe(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("tunnel unreachable at %s: %w", addr, err)
	}
	conn.Close()
	return nil
}

// ProbeHealth verifies that a cc-clip daemon is actually answering at addr. It
// first checks TCP reachability (so a closed port reports a plain unreachable
// error), then performs an HTTP GET /health. When TCP succeeds but the HTTP
// probe fails, it wraps ErrDaemonNotAnswering so callers can distinguish a
// stale RemoteForward listener from a healthy daemon.
func ProbeHealth(addr string, timeout time.Duration) error {
	if err := Probe(addr, timeout); err != nil {
		return err
	}

	client := &http.Client{Timeout: timeout}
	url := "http://" + addr + "/health"
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("%w at %s: %v", ErrDaemonNotAnswering, addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w at %s: GET /health -> %d", ErrDaemonNotAnswering, addr, resp.StatusCode)
	}
	return nil
}
