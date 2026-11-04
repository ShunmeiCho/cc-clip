package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestTemporaryServerClosesAuthenticatedKeepAliveOnCancellation(t *testing.T) {
	srv, token := newTestServer(&mockClipboard{clipType: ClipboardInfo{Type: ClipboardEmpty}})
	listener, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.ServeListenerContext(ctx, listener) }()
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "GET /clipboard/type HTTP/1.1\r\nHost: localhost\r\nUser-Agent: cc-clip/win-bridge\r\nAuthorization: Bearer %s\r\n\r\n", token)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Close {
		t.Fatalf("expected authenticated keep-alive response: %v", resp)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("temporary server did not stop")
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("authenticated connection survived cancellation: %v", err)
	}
}
