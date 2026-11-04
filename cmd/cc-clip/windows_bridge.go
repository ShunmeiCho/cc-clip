package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/winbridge"
)

type windowsBridgeOptions struct {
	Port      int
	TokenFile string
	Probe     bool
	ReadImage bool
	Agent     []string
}

func parseWindowsBridge(args []string) (windowsBridgeOptions, error) {
	opts := windowsBridgeOptions{Port: 18339}
	fs := flag.NewFlagSet("windows-bridge", flag.ContinueOnError)
	fs.IntVar(&opts.Port, "port", 18339, "loopback tunnel port")
	fs.StringVar(&opts.TokenFile, "token-file", "", "private bridge token file")
	fs.BoolVar(&opts.Probe, "probe", false, "report this process's window station")
	fs.BoolVar(&opts.ReadImage, "read-image", false, "diagnose native PNG and DIBV5 image reads")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	opts.Agent = fs.Args()
	if opts.Port < 1 || opts.Port > 65535 {
		return opts, fmt.Errorf("invalid bridge port")
	}
	if !opts.Probe && opts.TokenFile == "" {
		return opts, fmt.Errorf("--token-file is required")
	}
	if opts.Probe && len(opts.Agent) != 0 {
		return opts, fmt.Errorf("--probe cannot launch an agent")
	}
	if opts.ReadImage && !opts.Probe {
		return opts, fmt.Errorf("--read-image requires --probe")
	}
	return opts, nil
}

func cmdWindowsBridge() {
	opts, err := parseWindowsBridge(os.Args[2:])
	if err != nil {
		log.Fatal(err)
	}
	if opts.Probe {
		info, err := winbridge.Station()
		if err != nil {
			log.Fatal(err)
		}
		if opts.ReadImage {
			if err := nativeImageDiagnostics(info); err != nil {
				log.Fatal(err)
			}
		}
		json.NewEncoder(os.Stdout).Encode(info)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer cancel()
	if err := runWindowsBridge(ctx, opts); err != nil {
		log.Fatal(err)
	}
}

func nativeImageDiagnostics(info map[string]any) error {
	pngData, err := winbridge.ReadFormat("png")
	if err != nil {
		return err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(pngData))
	if err != nil {
		return err
	}
	dib, err := winbridge.ReadFormat("dibv5")
	if err != nil {
		return err
	}
	if len(dib) < 124 || binary.LittleEndian.Uint32(dib[:4]) != 124 ||
		int(binary.LittleEndian.Uint32(dib[4:8])) != config.Width || int(binary.LittleEndian.Uint32(dib[8:12])) != config.Height {
		return fmt.Errorf("native DIBV5 does not match PNG dimensions")
	}
	hash := sha256.Sum256(pngData)
	info["image_width"], info["image_height"], info["png_sha256"] = config.Width, config.Height, hex.EncodeToString(hash[:])
	info["native_formats"] = []string{"PNG", "CF_DIBV5"}
	return nil
}

func runWindowsBridge(ctx context.Context, opts windowsBridgeOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready, done := make(chan error, 1), make(chan error, 1)
	go func() { done <- winbridge.Run(ctx, winbridge.NewHTTPSource(opts.Port, opts.TokenFile), ready) }()
	if err := <-ready; err != nil {
		return err
	}
	info, err := winbridge.Station()
	if err != nil {
		return err
	}
	log.Printf("windows-bridge: ready in window station %v (session %v)", info["window_station"], info["session_id"])
	if len(opts.Agent) == 0 {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			cancel()
			return <-done
		}
	}
	agent, err := bridgeAgentCommand(ctx, opts.Agent)
	if err != nil {
		return err
	}
	agent.Stdin, agent.Stdout, agent.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := agent.Start(); err != nil {
		return fmt.Errorf("start remote agent: %w", err)
	}
	agentDone := make(chan error, 1)
	go func() { agentDone <- agent.Wait() }()
	select {
	case err = <-agentDone:
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return err
	case err = <-done:
		cancel()
		<-agentDone
		return fmt.Errorf("clipboard bridge stopped: %v", err)
	}
}

// Resolve only executables. .cmd/.bat wrappers need cmd.exe semantics and must
// be launched explicitly; a user-controlled agent argument is never shell code.
func bridgeAgentCommand(ctx context.Context, args []string) (*exec.Cmd, error) {
	var path string
	var err error
	if args[0] == "@self" {
		path, err = os.Executable()
	} else {
		path, err = exec.LookPath(args[0])
	}
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".cmd" || ext == ".bat" {
		return nil, fmt.Errorf("agent resolves to a batch wrapper; launch its .exe or specify cmd.exe explicitly")
	}
	return exec.CommandContext(ctx, path, args[1:]...), nil
}
