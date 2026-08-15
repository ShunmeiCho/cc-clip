package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	var health struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&health); err != nil {
		return fmt.Errorf("%w at %s: invalid /health body: %v", ErrDaemonNotAnswering, addr, err)
	}
	if health.Service != "cc-clip" {
		return fmt.Errorf("%w at %s: /health service %q", ErrDaemonNotAnswering, addr, health.Service)
	}
	if health.Status != "ok" {
		return fmt.Errorf("%w at %s: /health status %q", ErrDaemonNotAnswering, addr, health.Status)
	}
	return nil
}

// HealthInfo is the decoded body of the daemon's GET /health endpoint.
// Version is empty on daemons that predate #22 P2.
type HealthInfo struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Version string `json:"version"`
}

// FetchHealth returns the daemon's /health identity. Unlike ProbeHealth it
// does not judge — it hands the decoded body to the caller, which is what
// `cc-clip status` needs to compare the running daemon's version against its
// own binary after an in-place upgrade.
func FetchHealth(addr string, timeout time.Duration) (HealthInfo, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return HealthInfo{}, fmt.Errorf("GET /health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return HealthInfo{}, fmt.Errorf("GET /health -> %d", resp.StatusCode)
	}
	var h HealthInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&h); err != nil {
		return HealthInfo{}, fmt.Errorf("invalid /health body: %w", err)
	}
	return h, nil
}
