package tunnel

import (
	"fmt"
	"net"
	"time"
)

func Probe(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("tunnel unreachable at %s: %w", addr, err)
	}
	conn.Close()
	return nil
}
