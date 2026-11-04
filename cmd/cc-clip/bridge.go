package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

type bridgeOptions struct {
	Host       string
	RemotePort int
	Agent      []string
	NoTTY      bool
	Check      bool
}

func parseBridge(args []string) (bridgeOptions, error) {
	var opts bridgeOptions
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return opts, fmt.Errorf("usage: cc-clip bridge HOST [--remote-port PORT] [--no-tty] -- AGENT [ARGS...]")
	}
	opts.Host = args[0]
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	fs.IntVar(&opts.RemotePort, "remote-port", 0, "remote loopback port (default: automatically selected)")
	fs.BoolVar(&opts.NoTTY, "no-tty", false, "noninteractive diagnostic command")
	fs.BoolVar(&opts.Check, "check", false, "verify PNG and DIBV5 native clipboard reads")
	if err := fs.Parse(args[1:]); err != nil {
		return opts, err
	}
	opts.Agent = fs.Args()
	if opts.Check {
		if len(opts.Agent) != 0 {
			return opts, fmt.Errorf("--check cannot launch an agent")
		}
		opts.Agent = []string{"@self", "windows-bridge", "--probe", "--read-image"}
		opts.NoTTY = true
	}
	if opts.RemotePort < 0 || opts.RemotePort > 65535 {
		return opts, fmt.Errorf("invalid remote port")
	}
	if len(opts.Agent) == 0 {
		return opts, fmt.Errorf("specify the remote agent after --")
	}
	return opts, nil
}

func cmdBridge() {
	opts, err := parseBridge(os.Args[2:])
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer cancel()
	if err := runBridge(ctx, opts); err != nil {
		log.Fatal(err)
	}
}

// privateSSHConfig snapshots the resolved host settings and strips forwarding
// and multiplexing. The bridge owns exactly one forward without rewriting the
// user's config or relying on Windows OpenSSH ControlMaster support.
func privateSSHConfig(host string) (string, func(), error) {
	cmd := exec.Command("ssh", "-G", "--", host)
	hideConsoleWindow(cmd)
	data, err := cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("resolve SSH host: %w", err)
	}
	config := bridgeSSHConfig(string(data))
	dir, err := os.MkdirTemp("", "cc-clip-bridge-")
	if err != nil {
		return "", nil, err
	}
	file := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(file, []byte(config), 0600); err != nil {
		os.Remove(dir)
		return "", nil, err
	}
	return file, func() { os.Remove(file); os.Remove(dir) }, nil
}

func bridgeSSHConfig(effective string) string {
	var out strings.Builder
	out.WriteString("Host *\n")
	for _, line := range strings.Split(effective, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "host", "localforward", "remoteforward", "dynamicforward", "controlmaster", "controlpath", "controlpersist", "clearallforwardings", "exitonforwardfailure", "remotecommand", "requesttty", "sessiontype", "serveraliveinterval", "serveralivecountmax":
			continue
		}
		if strings.EqualFold(fields[0], "identityfile") || strings.EqualFold(fields[0], "certificatefile") {
			value := strings.TrimSpace(strings.TrimSpace(line)[len(fields[0]):])
			out.WriteString("  " + fields[0] + " \"" + strings.ReplaceAll(value, "\"", "\\\"") + "\"\n")
			continue
		}
		out.WriteString("  " + strings.TrimSpace(line) + "\n")
	}
	out.WriteString("  ControlMaster no\n  ControlPath none\n  ExitOnForwardFailure yes\n  ServerAliveInterval 15\n  ServerAliveCountMax 3\n")
	return out.String()
}

func runBridge(ctx context.Context, opts bridgeOptions) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	return runBridgeWithClipboard(ctx, opts, daemon.NewClipboardReader(), binary)
}

func runBridgeWithClipboard(ctx context.Context, opts bridgeOptions, clipboard daemon.ClipboardReader, binary string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if runtime.GOOS != "windows" {
		return fmt.Errorf("this bridge currently requires a local Windows machine")
	}
	p, err := probeWindowsUploadHost(opts.Host)
	if err != nil {
		return err
	}
	if runtime.GOARCH != p.Arch {
		return fmt.Errorf("local and remote Windows architectures must match")
	}
	helper, err := ensureWindowsUploadHelperBinary(opts.Host, p, binary)
	if err != nil {
		return err
	}
	config, cleanupConfig, err := privateSSHConfig(opts.Host)
	if err != nil {
		return err
	}
	defer cleanupConfig()
	tm := token.NewEphemeralManager(time.Hour)
	sess, err := tm.Generate()
	if err != nil {
		return err
	}
	srv := daemon.NewServer("127.0.0.1:0", clipboard, tm, session.NewStore(time.Hour))
	srv.SetVersion(version)
	listener, err := srv.Listen()
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		if err := srv.ServeListenerContext(ctx, listener); err != nil && !strings.Contains(err.Error(), "closed") {
			log.Printf("bridge local daemon: %v", err)
		}
	}()
	localPort := listener.Addr().(*net.TCPAddr).Port
	idBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, idBytes); err != nil {
		return err
	}
	id := hex.EncodeToString(idBytes)
	expected := strings.TrimRight(p.Cache, `\/`) + `\cc-clip\bridge\` + id + `\token`
	defer func() {
		cancel()
		script := `if (Test-Path -LiteralPath ` + psLiteral(expected) + `) { Remove-Item -LiteralPath ` + psLiteral(expected) + ` }`
		if _, err := runUploadSSHTimeout(opts.Host, windowsEncodedCommand(script), nil, 10*time.Second); err != nil {
			log.Printf("bridge: could not remove expired remote token file: %v", err)
		}
	}()
	script := `& ` + psLiteral(helper) + ` remote bridge-token --id ` + psLiteral(id) + `; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }`
	out, err := runUploadSSH(opts.Host, windowsEncodedCommand(script), strings.NewReader(sess.Token))
	if err != nil {
		return err
	}
	var receipt struct {
		Path string `json:"path"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(out, &receipt); err != nil {
		return err
	}
	if !strings.EqualFold(receipt.Path, expected) {
		return fmt.Errorf("invalid bridge token deployment receipt")
	}
	if opts.RemotePort == 0 {
		if receipt.Port < 1 || receipt.Port > 65535 {
			return fmt.Errorf("invalid remote bridge port")
		}
		opts.RemotePort = receipt.Port
	}
	command := windowsBridgeRemoteCommand(helper, expected, opts.RemotePort, opts.Agent)
	args := []string{"-F", config, "-o", "ExitOnForwardFailure=yes", "-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", opts.RemotePort, localPort)}
	if !opts.NoTTY {
		args = append(args, "-tt")
	}
	args = append(args, "--", opts.Host, windowsEncodedCommand(command))
	ssh := exec.CommandContext(ctx, "ssh", args...)
	ssh.Stdin, ssh.Stdout, ssh.Stderr = os.Stdin, os.Stdout, os.Stderr
	log.Printf("bridge: launching %s on %s with native image clipboard", opts.Agent[0], opts.Host)
	return ssh.Run()
}

func windowsBridgeRemoteCommand(helper, tokenFile string, port int, agent []string) string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "& %s windows-bridge --port %d --token-file %s --", psLiteral(helper), port, psLiteral(tokenFile))
	for _, arg := range agent {
		out.WriteByte(' ')
		out.WriteString(psLiteral(arg))
	}
	out.WriteString("; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }")
	return out.String()
}
