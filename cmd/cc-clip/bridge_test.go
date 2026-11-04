package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

func TestBridgeOptionsAndAgentQuoting(t *testing.T) {
	opts, err := parseBridge([]string{"host", "--remote-port", "18339", "--", "codex.exe", "-C", `C:\My Project;$(whoami)`})
	if err != nil || opts.RemotePort != 18339 || len(opts.Agent) != 3 {
		t.Fatalf("invalid options: %+v %v", opts, err)
	}
	command := windowsBridgeRemoteCommand(`C:\Users\O'Brien\cc-clip.exe`, `C:\token`, 18339, opts.Agent)
	if !strings.Contains(command, "O''Brien") || !strings.Contains(command, `'C:\My Project;$(whoami)'`) {
		t.Fatalf("unquoted agent command: %s", command)
	}
	check, err := parseBridge([]string{"host", "--check"})
	if err != nil || !check.NoTTY || check.Agent[0] != "@self" {
		t.Fatalf("invalid check: %+v %v", check, err)
	}
	for _, args := range [][]string{{}, {"host"}, {"host", "--remote-port", "65536", "--", "codex"}, {"host", "--check", "--", "codex"}} {
		if _, err := parseBridge(args); err == nil {
			t.Fatalf("accepted invalid bridge args: %v", args)
		}
	}
}

func TestBridgeSSHConfigRemovesOtherForwardsAndMultiplexing(t *testing.T) {
	effective := "host server\nhostname 192.0.2.1\nuser alice\nidentityfile C:/Users/Alice Smith/.ssh/id_key\nremoteforward 18339 127.0.0.1:18339\nlocalforward 8080 localhost:80\ndynamicforward 1080\ncontrolpath ~/.ssh/master\ncontrolmaster auto\nremotecommand bash\nproxycommand ssh gateway -W %h:%p\nserveraliveinterval 0\nserveralivecountmax 99\n"
	got := bridgeSSHConfig(effective)
	for _, unwanted := range []string{"remoteforward", "localforward", "dynamicforward", "controlmaster auto", "remotecommand bash", "serveraliveinterval 0", "serveralivecountmax 99"} {
		if strings.Contains(strings.ToLower(got), unwanted) {
			t.Fatalf("inherited competing SSH option: %s", unwanted)
		}
	}
	if !strings.Contains(got, "proxycommand ssh gateway -W %h:%p") || !strings.Contains(got, `identityfile "C:/Users/Alice Smith/.ssh/id_key"`) || !strings.Contains(got, "ExitOnForwardFailure yes") {
		t.Fatalf("lost SSH authentication/routing: %s", got)
	}
}

type bridgeTestClipboard struct{ Data []byte }

func (c bridgeTestClipboard) Type() (daemon.ClipboardInfo, error) {
	return daemon.ClipboardInfo{Type: daemon.ClipboardImage, Format: "png", Revision: 1}, nil
}
func (c bridgeTestClipboard) ImageBytes() ([]byte, error) { return c.Data, nil }
func (c bridgeTestClipboard) Text() (string, error)       { return "", nil }

// Explicit opt-in: this launches an SSH child but never changes the local
// user's clipboard and never calls an AI model. The independent remote native
// consumer requests both delayed formats in the agent's inherited station.
func TestBridgeRemoteNativeClipboard(t *testing.T) {
	host, binary := os.Getenv("CC_CLIP_BRIDGE_SMOKE_HOST"), os.Getenv("CC_CLIP_BRIDGE_SMOKE_BINARY")
	if host == "" || binary == "" || runtime.GOOS != "windows" {
		t.Skip("set CC_CLIP_BRIDGE_SMOKE_HOST and CC_CLIP_BRIDGE_SMOKE_BINARY for Windows SSH smoke")
	}
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.NRGBA{R: 255, A: 128})
	img.Set(2, 1, color.NRGBA{B: 200, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := runBridgeWithClipboard(ctx, bridgeOptions{Host: host, NoTTY: true, Agent: []string{"@self", "windows-bridge", "--probe", "--read-image"}}, bridgeTestClipboard{data.Bytes()}, binary); err != nil {
		t.Fatal(err)
	}
}

type changingBridgeClipboard struct {
	mu            sync.Mutex
	images        [2][]byte
	readAt        [2]time.Time
	failAfterRead bool
}

func (c *changingBridgeClipboard) phase() int {
	if !c.readAt[1].IsZero() && time.Since(c.readAt[1]) > 750*time.Millisecond {
		return 2
	}
	if !c.readAt[0].IsZero() && time.Since(c.readAt[0]) > 750*time.Millisecond {
		return 1
	}
	return 0
}

func (c *changingBridgeClipboard) Type() (daemon.ClipboardInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	phase := c.phase()
	if c.failAfterRead && phase > 0 {
		return daemon.ClipboardInfo{}, fmt.Errorf("simulated unavailable clipboard source")
	}
	if phase == 2 {
		return daemon.ClipboardInfo{Type: daemon.ClipboardText, Revision: 3}, nil
	}
	return daemon.ClipboardInfo{Type: daemon.ClipboardImage, Format: "png", Revision: uint32(phase + 1)}, nil
}

func (c *changingBridgeClipboard) ImageBytes() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	phase := c.phase()
	if phase == 2 {
		return nil, nil
	}
	if c.readAt[phase].IsZero() {
		c.readAt[phase] = time.Now()
	}
	return c.images[phase], nil
}

func (c *changingBridgeClipboard) Text() (string, error) { return "unmirrored test text", nil }

// A separate .NET clipboard consumer verifies synthesized image formats,
// revision refresh, and removal when the local source switches to text.
func TestBridgeRemoteClipboardUpdatesAndClearsImages(t *testing.T) {
	host, binary := os.Getenv("CC_CLIP_BRIDGE_SMOKE_HOST"), os.Getenv("CC_CLIP_BRIDGE_SMOKE_BINARY")
	if host == "" || binary == "" || runtime.GOOS != "windows" {
		t.Skip("set CC_CLIP_BRIDGE_SMOKE_HOST and CC_CLIP_BRIDGE_SMOKE_BINARY for Windows SSH smoke")
	}
	clip := &changingBridgeClipboard{}
	for phase, bounds := range []image.Rectangle{image.Rect(0, 0, 3, 2), image.Rect(0, 0, 4, 1)} {
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewNRGBA(bounds)); err != nil {
			t.Fatal(err)
		}
		clip.images[phase] = data.Bytes()
	}
	script := `Add-Type -AssemblyName System.Windows.Forms; ` +
		`$a=[System.Windows.Forms.Clipboard]::GetImage(); if ($null -eq $a -or $a.Width -ne 3 -or $a.Height -ne 2) { throw 'first native image mismatch' }; $a.Dispose(); ` +
		`Start-Sleep -Milliseconds 1500; $b=[System.Windows.Forms.Clipboard]::GetImage(); if ($null -eq $b -or $b.Width -ne 4 -or $b.Height -ne 1) { throw 'clipboard revision did not refresh' }; $b.Dispose(); ` +
		`Start-Sleep -Milliseconds 1500; if ([System.Windows.Forms.Clipboard]::ContainsImage()) { throw 'stale image retained after switch to text' }; ` +
		`if ([System.Windows.Forms.Clipboard]::ContainsText()) { throw 'text unexpectedly mirrored' }; [Console]::WriteLine('PASS native image update and removal')`
	// windowsEncodedCommand is an SSH shell command string; here exec launches
	// PowerShell directly with the encoded payload as a separate argument.
	encoded := strings.Fields(windowsEncodedCommand(script))
	agent := []string{"powershell.exe", "-NoLogo", "-NoProfile", "-STA", "-NonInteractive", "-EncodedCommand", encoded[len(encoded)-1]}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := runBridgeWithClipboard(ctx, bridgeOptions{Host: host, NoTTY: true, Agent: agent}, clip, binary); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeRemoteClipboardFailureStopsAgent(t *testing.T) {
	host, binary := os.Getenv("CC_CLIP_BRIDGE_SMOKE_HOST"), os.Getenv("CC_CLIP_BRIDGE_SMOKE_BINARY")
	if host == "" || binary == "" || runtime.GOOS != "windows" {
		t.Skip("set CC_CLIP_BRIDGE_SMOKE_HOST and CC_CLIP_BRIDGE_SMOKE_BINARY for Windows SSH smoke")
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	clip := &changingBridgeClipboard{failAfterRead: true}
	clip.images[0] = data.Bytes()
	script := `Add-Type -AssemblyName System.Windows.Forms; $a=[System.Windows.Forms.Clipboard]::GetImage(); ` +
		`if ($null -eq $a -or $a.Width -ne 3) { throw 'first native image mismatch' }; $a.Dispose(); ` +
		`Start-Sleep -Seconds 30; throw 'agent survived unavailable clipboard source'`
	encoded := strings.Fields(windowsEncodedCommand(script))
	agent := []string{"powershell.exe", "-NoProfile", "-STA", "-NonInteractive", "-EncodedCommand", encoded[len(encoded)-1]}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := runBridgeWithClipboard(ctx, bridgeOptions{Host: host, NoTTY: true, Agent: agent}, clip, binary)
	clip.mu.Lock()
	readAt := clip.readAt[0]
	clip.mu.Unlock()
	if err == nil || readAt.IsZero() || time.Since(readAt) < 10*time.Second || time.Since(readAt) > 25*time.Second {
		t.Fatalf("agent failed to stop after source loss: error=%v firstRead=%v elapsed=%v", err, readAt, time.Since(readAt))
	}
}
