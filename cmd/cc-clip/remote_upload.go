package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shunmei/cc-clip/internal/remoteupload"
)

type uploadProbe struct {
	Protocol int    `json:"protocol"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Home     string `json:"home"`
	Cache    string `json:"cache"`
}

// cmdRemote is an SSH helper, not a daemon. It reads one frame then exits.
func cmdRemote() {
	if err := runRemote(os.Args[2:], os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func runRemote(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cc-clip remote probe|upload")
	}
	switch args[0] {
	case "probe":
		if len(args) != 1 {
			return fmt.Errorf("remote probe takes no arguments")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(uploadProbe{remoteupload.ProtocolVersion, runtime.GOOS, runtime.GOARCH, home, cache})
	case "upload":
		fs := flag.NewFlagSet("remote upload", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dir := fs.String("remote-dir", "", "upload directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if len(fs.Args()) != 0 {
			return fmt.Errorf("unexpected remote upload arguments")
		}
		root, err := receiverUploadDirectory(*dir)
		if err != nil {
			return err
		}
		result, err := remoteupload.Receive(in, root)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	case "bridge-token":
		fs := flag.NewFlagSet("remote bridge-token", flag.ContinueOnError)
		id := fs.String("id", "", "private bridge session identifier")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		idBytes, err := hex.DecodeString(*id)
		if err != nil || len(idBytes) != 16 || len(fs.Args()) != 0 {
			return fmt.Errorf("invalid bridge session identifier")
		}
		data, err := io.ReadAll(io.LimitReader(in, 65))
		if err != nil {
			return err
		}
		tokenBytes, err := hex.DecodeString(string(data))
		if err != nil || len(tokenBytes) != 32 {
			return fmt.Errorf("invalid bridge token")
		}
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		path, err := remoteupload.WriteSecret(filepath.Join(cache, "cc-clip", "bridge", *id), "token", data)
		if err != nil {
			return err
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			os.Remove(path)
			return err
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		return json.NewEncoder(out).Encode(map[string]any{"path": path, "port": port})
	default:
		return fmt.Errorf("unknown remote command: %s", args[0])
	}
}

func receiverUploadDirectory(dir string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	managed := filepath.Join(cache, "cc-clip", "uploads")
	if dir == "" || dir == defaultRemoteUploadDir {
		return managed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if dir == "~" {
		return "", fmt.Errorf("use a dedicated upload directory, not your home directory")
	}
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(dir)
		if (vol != "" && !filepath.IsAbs(dir)) || strings.Contains(strings.TrimPrefix(dir, vol), ":") {
			return "", fmt.Errorf("Windows upload directory cannot be drive-relative or contain an alternate data stream")
		}
	}
	if len(dir) >= 2 && dir[0] == '~' && (dir[1] == '/' || dir[1] == '\\') {
		dir = filepath.Join(home, dir[2:])
	} else if !filepath.IsAbs(dir) {
		if runtime.GOOS == "windows" {
			dir = filepath.Join(managed, dir)
		} else {
			dir = filepath.Join(home, dir)
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	// Windows permissions are applied to this directory. Only permit the
	// managed tree so a custom path cannot change a shared directory's ACL.
	if runtime.GOOS == "windows" && !strings.EqualFold(dir, managed) &&
		!strings.HasPrefix(strings.ToLower(dir), strings.ToLower(managed+string(os.PathSeparator))) {
		return "", fmt.Errorf("Windows --remote-dir must be within %s", managed)
	}
	return dir, nil
}
