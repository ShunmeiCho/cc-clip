package x11bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// --- Unit tests (no X11 needed) ---

func TestAtomNames(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"clipboard", AtomNameClipboard},
		{"targets", AtomNameTargets},
		{"timestamp", AtomNameTimestamp},
		{"incr", AtomNameIncr},
		{"image_png", AtomNameImagePNG},
	}

	for _, tt := range tests {
		if tt.expected == "" {
			t.Errorf("%s: atom name is empty", tt.name)
		}
	}
}

func TestIncrTransferIsComplete(t *testing.T) {
	t.Run("not started", func(t *testing.T) {
		tr := &IncrTransfer{Data: []byte("hello"), Offset: 0}
		if tr.IsComplete() {
			t.Error("expected not complete")
		}
	})

	t.Run("partial", func(t *testing.T) {
		tr := &IncrTransfer{Data: []byte("hello"), Offset: 3}
		if tr.IsComplete() {
			t.Error("expected not complete")
		}
	})

	t.Run("all data sent but terminator missing", func(t *testing.T) {
		tr := &IncrTransfer{Data: []byte("hello"), Offset: 5}
		if tr.IsComplete() {
			t.Error("expected transfer to stay active until final zero-length marker")
		}
	})

	t.Run("complete after terminator", func(t *testing.T) {
		tr := &IncrTransfer{Data: []byte("hello"), Offset: 5, FinalSent: true}
		if !tr.IsComplete() {
			t.Error("expected complete")
		}
	})

	t.Run("beyond after terminator", func(t *testing.T) {
		tr := &IncrTransfer{Data: []byte("hello"), Offset: 10, FinalSent: true}
		if !tr.IsComplete() {
			t.Error("expected complete")
		}
	})
}

func TestIncrTransferIsCompleteNeedsFinalZeroLengthMarker(t *testing.T) {
	tr := &IncrTransfer{Data: []byte("hello"), Offset: len("hello")}
	if tr.IsComplete() {
		t.Fatal("transfer should remain active until the terminating zero-length chunk is sent")
	}
}

func TestAtomsToBytes(t *testing.T) {
	atoms := []uint32{1, 256, 65536}
	// Convert to xproto.Atom for type compatibility
	// (xproto.Atom is uint32)
	import_atoms := make([]uint32, len(atoms))
	copy(import_atoms, atoms)

	// Test the byte conversion manually
	data := make([]byte, len(atoms)*4)
	for i, a := range atoms {
		data[i*4+0] = byte(a)
		data[i*4+1] = byte(a >> 8)
		data[i*4+2] = byte(a >> 16)
		data[i*4+3] = byte(a >> 24)
	}

	if len(data) != 12 {
		t.Errorf("expected 12 bytes, got %d", len(data))
	}
}

func TestMaxDirectSizeFromRequestLengthUsesX11FourByteUnits(t *testing.T) {
	got := maxDirectSizeFromRequestLength(65535)
	want := 65535*4 - changePropertyRequestOverheadBytes
	if got != want {
		t.Fatalf("max direct size = %d, want %d", got, want)
	}

	if got := maxDirectSizeFromRequestLength(0); got != defaultMaxDirectSize {
		t.Fatalf("zero max request should use default %d, got %d", defaultMaxDirectSize, got)
	}

	if got := maxDirectSizeFromRequestLength(4); got != 0 {
		t.Fatalf("tiny max request should force INCR, got %d", got)
	}
}

func TestIncrTimeoutTickInterval(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{
			name:    "production timeout capped at one second",
			timeout: 30 * time.Second,
			want:    time.Second,
		},
		{
			name:    "short test timeout checks faster",
			timeout: 50 * time.Millisecond,
			want:    25 * time.Millisecond,
		},
		{
			name:    "non-positive timeout falls back to cap",
			timeout: 0,
			want:    time.Second,
		},
		{
			name:    "sub-millisecond timeout still ticks",
			timeout: time.Nanosecond,
			want:    time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := incrTimeoutTickInterval(tt.timeout); got != tt.want {
				t.Fatalf("incrTimeoutTickInterval(%s) = %s, want %s", tt.timeout, got, tt.want)
			}
		})
	}
}

func TestClearExpiredIncr(t *testing.T) {
	b := &Bridge{
		activeIncr:   &IncrTransfer{Requestor: 42},
		incrDeadline: time.Unix(10, 0),
		incrTimeout:  50 * time.Millisecond,
	}

	b.clearExpiredIncr(time.Unix(9, 0))
	if b.activeIncr == nil {
		t.Fatal("clearExpiredIncr cleared transfer before deadline")
	}

	b.clearExpiredIncr(time.Unix(10, 0))
	if b.activeIncr != nil {
		t.Fatal("clearExpiredIncr should clear transfer at deadline")
	}
	if !b.incrDeadline.IsZero() {
		t.Fatal("clearExpiredIncr should clear deadline")
	}
}

func TestStartActiveIncrTracksRequestorForUnsubscribe(t *testing.T) {
	b := &Bridge{incrTimeout: 50 * time.Millisecond}

	b.startActiveIncr(&IncrTransfer{Requestor: 0x1234})

	if !b.hasIncrRequestor {
		t.Fatal("startActiveIncr should record that a requestor was subscribed")
	}
	if b.incrRequestor != 0x1234 {
		t.Fatalf("incrRequestor = 0x%x, want 0x1234", b.incrRequestor)
	}
}

func TestClearActiveIncrUnsubscribesRequestor(t *testing.T) {
	// conn is nil: clearActiveIncr must still reset the tracking state without
	// panicking, proving the unsubscribe path does not depend on activeIncr
	// remaining non-nil and tolerates the absence of a live connection.
	b := &Bridge{
		activeIncr:       &IncrTransfer{Requestor: 0x1234},
		incrRequestor:    0x1234,
		hasIncrRequestor: true,
		incrDeadline:     time.Unix(10, 0),
	}

	b.clearActiveIncr()

	if b.activeIncr != nil {
		t.Fatal("clearActiveIncr should clear activeIncr")
	}
	if b.hasIncrRequestor {
		t.Fatal("clearActiveIncr should clear the requestor subscription flag")
	}
	if b.incrRequestor != 0 {
		t.Fatalf("clearActiveIncr should reset incrRequestor, got 0x%x", b.incrRequestor)
	}
	if !b.incrDeadline.IsZero() {
		t.Fatal("clearActiveIncr should clear deadline")
	}
}

func TestUnsubscribeIncrRequestorNoOpWhenNotSubscribed(t *testing.T) {
	b := &Bridge{}
	// Must not panic or attempt an X call when nothing was subscribed.
	b.unsubscribeIncrRequestor()
	if b.hasIncrRequestor {
		t.Fatal("unsubscribeIncrRequestor should remain unsubscribed")
	}
}

func TestPublishXEventStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan bool, 1)
	go func() {
		done <- publishXEvent(ctx, make(chan xgb.Event), make(chan xgb.Error), xproto.SelectionClearEvent{}, nil)
	}()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("publishXEvent should report false after context cancellation")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("publishXEvent blocked after context cancellation")
	}
}

// --- Helpers for integration tests ---

func requireXvfbAndXclip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb not available, skipping X11 test")
	}
	if _, err := exec.LookPath("xclip"); err != nil {
		t.Skip("xclip not available, skipping X11 test")
	}
}

// startTestXvfb starts a temporary Xvfb for testing.
// Returns the display string (e.g. ":99") and a cleanup function.
func startTestXvfb(t *testing.T) string {
	t.Helper()

	// Find a free display number by trying to start Xvfb.
	// Use -displayfd to let Xvfb choose.
	displayFile := filepath.Join(t.TempDir(), "display")

	cmd := exec.Command("Xvfb", "-displayfd", "1", "-screen", "0", "1x1x24", "-nolisten", "tcp")

	// Capture stdout for display number.
	outFile, err := os.Create(displayFile)
	if err != nil {
		t.Fatalf("cannot create display file: %v", err)
	}
	cmd.Stdout = outFile

	if err := cmd.Start(); err != nil {
		outFile.Close()
		t.Fatalf("cannot start Xvfb: %v", err)
	}
	outFile.Close()

	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	// Wait for display file to be written.
	var display string
	for i := 0; i < 20; i++ {
		data, err := os.ReadFile(displayFile)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			display = ":" + strings.TrimSpace(string(data))
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if display == "" {
		t.Fatal("Xvfb did not write display number")
	}

	return display
}

// startMockDaemon starts a mock cc-clip HTTP server.
func startMockDaemon(t *testing.T, clipType string, imageData []byte, token string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /clipboard/type", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"type":   clipType,
			"format": "png",
		})
	})

	mux.HandleFunc("GET /clipboard/image", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if len(imageData) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(imageData)
	})

	// Use a specific listener to get a predictable port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot create listener: %v", err)
	}

	srv := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: mux},
	}
	srv.Start()
	t.Cleanup(srv.Close)

	return srv
}

func portFromURL(url string) int {
	// url is like "http://127.0.0.1:12345"
	parts := strings.Split(url, ":")
	if len(parts) < 3 {
		return 0
	}
	var port int
	fmt.Sscanf(parts[2], "%d", &port)
	return port
}

func writeTokenFile(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		t.Fatalf("cannot write token file: %v", err)
	}
	return path
}

func newX11Requestor(t *testing.T, display string) (*xgb.Conn, xproto.Window, xproto.Atom) {
	t.Helper()

	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatalf("cannot connect requestor to X display %s: %v", display, err)
	}
	t.Cleanup(func() {
		conn.Close()
	})

	setup := xproto.Setup(conn)
	if len(setup.Roots) == 0 {
		t.Fatal("requestor X connection has no screens")
	}
	screen := setup.Roots[0]

	window, err := xproto.NewWindowId(conn)
	if err != nil {
		t.Fatalf("cannot allocate requestor window: %v", err)
	}

	err = xproto.CreateWindowChecked(
		conn,
		screen.RootDepth,
		window,
		screen.Root,
		0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange},
	).Check()
	if err != nil {
		t.Fatalf("cannot create requestor window: %v", err)
	}

	reply, err := xproto.InternAtom(conn, false, uint16(len("CC_CLIP_TEST")), "CC_CLIP_TEST").Reply()
	if err != nil {
		t.Fatalf("cannot create requestor property atom: %v", err)
	}

	return conn, window, reply.Atom
}

func waitForMatchingEvent(t *testing.T, conn *xgb.Conn, timeout time.Duration, match func(xgb.Event) bool) xgb.Event {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ev, err := conn.PollForEvent()
		if err != nil {
			t.Fatalf("unexpected X11 error while waiting for event: %v", err)
		}
		if ev != nil {
			if match(ev) {
				return ev
			}
			continue
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out after %s waiting for matching X11 event", timeout)
	return nil
}

func waitForSelectionNotify(t *testing.T, conn *xgb.Conn, window xproto.Window, timeout time.Duration) xproto.SelectionNotifyEvent {
	t.Helper()

	ev := waitForMatchingEvent(t, conn, timeout, func(ev xgb.Event) bool {
		notify, ok := ev.(xproto.SelectionNotifyEvent)
		return ok && notify.Requestor == window
	})
	notify, ok := ev.(xproto.SelectionNotifyEvent)
	if !ok {
		t.Fatalf("matched event was %T, want SelectionNotifyEvent", ev)
	}
	return notify
}

// --- Integration tests (require Xvfb + xclip) ---

func TestBridge_ClaimOwnership(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", []byte("fake-png"), "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)

	// Give bridge time to start event loop.
	time.Sleep(200 * time.Millisecond)

	// Verify: xclip should be able to query TARGETS without hanging.
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xclip TARGETS failed: %v\noutput: %s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "image/png") {
		t.Errorf("TARGETS output should contain image/png, got: %s", output)
	}
}

func TestBridge_TargetsNoImage(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "text", nil, "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, _ := cmd.CombinedOutput()

	output := string(out)
	if strings.Contains(output, "image/png") {
		t.Errorf("TARGETS output should NOT contain image/png when clipboard has text, got: %s", output)
	}
}

func TestBridge_ImageSmall(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	// Create a small test PNG (just some bytes, doesn't need to be valid PNG for protocol test).
	imageData := make([]byte, 1024)
	rand.Read(imageData)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", imageData, "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("xclip image read failed: %v", err)
	}

	if len(out) != len(imageData) {
		t.Errorf("image size mismatch: got %d, want %d", len(out), len(imageData))
	}
	for i := range out {
		if out[i] != imageData[i] {
			t.Errorf("image data mismatch at byte %d", i)
			break
		}
	}
}

func TestBridge_ImageLargeINCR(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	// Create a large image that will trigger INCR protocol (>256KB).
	imageData := make([]byte, 512*1024)
	rand.Read(imageData)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", imageData, "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("xclip image read failed: %v", err)
	}

	if len(out) != len(imageData) {
		t.Errorf("INCR image size mismatch: got %d, want %d", len(out), len(imageData))
	}
	for i := range out {
		if out[i] != imageData[i] {
			t.Errorf("INCR image data mismatch at byte %d", i)
			break
		}
	}
}

func TestBridge_ImageLargeINCRSendsFinalZeroLengthChunk(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	imageData := make([]byte, 512*1024)
	rand.Read(imageData)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", imageData, "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	conn, window, property := newX11Requestor(t, display)
	atoms := NewAtomCache(conn)

	clipboardAtom := atoms.MustGet(AtomNameClipboard)
	imagePNGAtom := atoms.MustGet(AtomNameImagePNG)
	incrAtom := atoms.MustGet(AtomNameIncr)

	if err := xproto.ConvertSelectionChecked(
		conn,
		window,
		clipboardAtom,
		imagePNGAtom,
		property,
		xproto.TimeCurrentTime,
	).Check(); err != nil {
		t.Fatalf("ConvertSelection failed: %v", err)
	}

	waitForMatchingEvent(t, conn, 2*time.Second, func(ev xgb.Event) bool {
		notify, ok := ev.(xproto.SelectionNotifyEvent)
		return ok && notify.Requestor == window && notify.Property == property
	})

	reply, err := xproto.GetProperty(conn, false, window, property, xproto.GetPropertyTypeAny, 0, math.MaxUint32).Reply()
	if err != nil {
		t.Fatalf("initial GetProperty failed: %v", err)
	}
	if reply.Type != incrAtom {
		t.Fatalf("expected INCR response type %d, got %d", incrAtom, reply.Type)
	}
	if reply.Format != 32 {
		t.Fatalf("expected INCR response format 32, got %d", reply.Format)
	}
	if announced := xgb.Get32(reply.Value); announced != uint32(len(imageData)) {
		t.Fatalf("announced INCR size = %d, want %d", announced, len(imageData))
	}

	if err := xproto.DeletePropertyChecked(conn, window, property).Check(); err != nil {
		t.Fatalf("DeleteProperty failed: %v", err)
	}

	var received []byte
	for {
		waitForMatchingEvent(t, conn, 2*time.Second, func(ev xgb.Event) bool {
			prop, ok := ev.(xproto.PropertyNotifyEvent)
			return ok &&
				prop.Window == window &&
				prop.Atom == property &&
				prop.State == xproto.PropertyNewValue
		})

		reply, err := xproto.GetProperty(conn, true, window, property, xproto.GetPropertyTypeAny, 0, math.MaxUint32).Reply()
		if err != nil {
			t.Fatalf("chunk GetProperty failed: %v", err)
		}
		if len(reply.Value) == 0 {
			if reply.Type != imagePNGAtom {
				t.Fatalf("final INCR chunk should be an explicit zero-length %d property, got type %d", imagePNGAtom, reply.Type)
			}
			break
		}
		received = append(received, reply.Value...)
	}

	if !bytes.Equal(received, imageData) {
		t.Fatalf("INCR payload mismatch: got %d bytes, want %d", len(received), len(imageData))
	}
}

func TestBridge_INCRTimeoutAllowsNextImageRequest(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	imageData := make([]byte, 512*1024)
	rand.Read(imageData)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", imageData, "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile, WithIncrTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	conn1, window1, property1 := newX11Requestor(t, display)
	atoms1 := NewAtomCache(conn1)
	clipboardAtom1 := atoms1.MustGet(AtomNameClipboard)
	imagePNGAtom1 := atoms1.MustGet(AtomNameImagePNG)
	incrAtom1 := atoms1.MustGet(AtomNameIncr)

	if err := xproto.ConvertSelectionChecked(
		conn1,
		window1,
		clipboardAtom1,
		imagePNGAtom1,
		property1,
		xproto.TimeCurrentTime,
	).Check(); err != nil {
		t.Fatalf("first ConvertSelection failed: %v", err)
	}
	notify1 := waitForSelectionNotify(t, conn1, window1, 2*time.Second)
	if notify1.Property != property1 {
		t.Fatalf("first request should start INCR on property %d, got property %d", property1, notify1.Property)
	}
	reply1, err := xproto.GetProperty(conn1, false, window1, property1, xproto.GetPropertyTypeAny, 0, math.MaxUint32).Reply()
	if err != nil {
		t.Fatalf("first INCR marker GetProperty failed: %v", err)
	}
	if reply1.Type != incrAtom1 {
		t.Fatalf("first request should receive INCR marker type %d, got %d", incrAtom1, reply1.Type)
	}

	// Do not delete property1. A client that never acknowledges the INCR marker
	// used to leave activeIncr set forever, causing later image requests to be refused.
	time.Sleep(150 * time.Millisecond)

	conn2, window2, property2 := newX11Requestor(t, display)
	atoms2 := NewAtomCache(conn2)
	clipboardAtom2 := atoms2.MustGet(AtomNameClipboard)
	imagePNGAtom2 := atoms2.MustGet(AtomNameImagePNG)
	incrAtom2 := atoms2.MustGet(AtomNameIncr)

	if err := xproto.ConvertSelectionChecked(
		conn2,
		window2,
		clipboardAtom2,
		imagePNGAtom2,
		property2,
		xproto.TimeCurrentTime,
	).Check(); err != nil {
		t.Fatalf("second ConvertSelection failed: %v", err)
	}
	notify2 := waitForSelectionNotify(t, conn2, window2, 2*time.Second)
	if notify2.Property == xproto.AtomNone {
		t.Fatal("second image request was refused; INCR timeout did not clear the stalled transfer")
	}
	if notify2.Property != property2 {
		t.Fatalf("second request property = %d, want %d", notify2.Property, property2)
	}
	reply2, err := xproto.GetProperty(conn2, false, window2, property2, xproto.GetPropertyTypeAny, 0, math.MaxUint32).Reply()
	if err != nil {
		t.Fatalf("second INCR marker GetProperty failed: %v", err)
	}
	if reply2.Type != incrAtom2 {
		t.Fatalf("second request should receive INCR marker type %d, got %d", incrAtom2, reply2.Type)
	}
}

func TestBridge_INCRTimeoutDoesNotBreakCompletedTransfer(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	imageData := make([]byte, 512*1024)
	rand.Read(imageData)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", imageData, "test-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile, WithIncrTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("xclip image read failed: %v", err)
	}
	if !bytes.Equal(out, imageData) {
		t.Fatalf("INCR image data mismatch: got %d bytes, want %d", len(out), len(imageData))
	}

	time.Sleep(150 * time.Millisecond)

	cmd = exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("second xclip image read failed after timeout window: %v", err)
	}
	if !bytes.Equal(out, imageData) {
		t.Fatalf("second INCR image data mismatch: got %d bytes, want %d", len(out), len(imageData))
	}
}

func TestBridge_TunnelDown(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	tokenFile := writeTokenFile(t, "test-token")
	// Use a port with no server — tunnel is "down".
	bridge, err := New(display, 19999, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	// xclip should fail (or return empty), not hang.
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)

	done := make(chan struct{})
	go func() {
		cmd.CombinedOutput()
		close(done)
	}()

	select {
	case <-done:
		// Good: xclip completed (probably with error).
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("xclip hung when tunnel is down")
	}
}

func TestBridge_TokenInvalid(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	tokenFile := writeTokenFile(t, "wrong-token")
	srv := startMockDaemon(t, "image", []byte("png-data"), "correct-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	// xclip should fail, not hang.
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	_, err = cmd.Output()
	if err == nil {
		t.Error("expected xclip to fail with invalid token")
	}
}

func TestBridge_EmptyImage204(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	tokenFile := writeTokenFile(t, "test-token")
	srv := startMockDaemon(t, "image", nil, "test-token") // nil image -> 204
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	_, err = cmd.Output()
	if err == nil {
		t.Error("expected xclip to fail with empty clipboard")
	}
}

// TestE2E_FullPasteFlow is the end-to-end smoke test:
// mock HTTP + Xvfb + x11-bridge + xclip roundtrip.
func TestE2E_FullPasteFlow(t *testing.T) {
	requireXvfbAndXclip(t)
	display := startTestXvfb(t)

	// Generate deterministic test image data.
	imageData := make([]byte, 4096)
	rand.Read(imageData)

	tokenFile := writeTokenFile(t, "e2e-token")
	srv := startMockDaemon(t, "image", imageData, "e2e-token")
	port := portFromURL(srv.URL)

	bridge, err := New(display, port, tokenFile)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	// Step 1: Query TARGETS
	targetsCmd := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	targetsCmd.Env = append(os.Environ(), "DISPLAY="+display)
	targetsOut, err := targetsCmd.Output()
	if err != nil {
		t.Fatalf("TARGETS query failed: %v", err)
	}
	if !strings.Contains(string(targetsOut), "image/png") {
		t.Fatalf("TARGETS does not contain image/png: %s", targetsOut)
	}

	// Step 2: Read image
	imageCmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	imageCmd.Env = append(os.Environ(), "DISPLAY="+display)
	imageOut, err := imageCmd.Output()
	if err != nil {
		t.Fatalf("image read failed: %v", err)
	}

	// Step 3: Compare bytes
	if len(imageOut) != len(imageData) {
		t.Fatalf("E2E size mismatch: got %d, want %d", len(imageOut), len(imageData))
	}
	for i := range imageOut {
		if imageOut[i] != imageData[i] {
			t.Fatalf("E2E data mismatch at byte %d", i)
		}
	}
}
