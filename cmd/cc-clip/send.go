package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

const defaultRemoteUploadDir = "~/.cache/cc-clip/uploads"

func cmdSend() {
	host := ""
	flagArgs := os.Args[2:]
	if len(os.Args) > 2 && !strings.HasPrefix(os.Args[2], "-") {
		host = os.Args[2]
		flagArgs = os.Args[3:]
	}

	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	localFile := fs.String("file", "", "upload this image file instead of reading the clipboard")
	remoteDir := fs.String("remote-dir", defaultRemoteUploadDir, "remote upload directory")
	paste := fs.Bool("paste", false, "paste the remote path into the active window")
	delayMS := fs.Int("delay-ms", 150, "delay before Ctrl+Shift+V when --paste is used")
	noRestore := fs.Bool("no-restore", false, "do not restore the original image clipboard after --paste")

	if err := fs.Parse(flagArgs); err != nil {
		log.Fatal(err)
	}
	if *delayMS < 0 {
		log.Fatalf("invalid --delay-ms: %d", *delayMS)
	}
	if host == "" {
		var ok bool
		var err error
		host, ok, err = defaultRemoteHost()
		if err != nil {
			log.Fatalf("cannot resolve default host: %v", err)
		}
		if !ok || host == "" {
			log.Fatal("usage: cc-clip send [<host>] [--file PATH] [--remote-dir DIR] [--paste] [--delay-ms N] [--no-restore]")
		}
	}
	restoreClipboard := !*noRestore

	result, err := uploadImage(host, *remoteDir, *localFile)
	if err != nil {
		log.Fatalf("send failed: %v", err)
	}
	if result.TempFile {
		defer os.Remove(result.LocalImagePath)
	}

	fmt.Println(result.RemotePath)

	if !*paste {
		return
	}

	if err := pasteRemotePath(result.RemotePath, result.LocalImagePath, time.Duration(*delayMS)*time.Millisecond, restoreClipboard); err != nil {
		log.Fatalf("send uploaded the image but failed to inject the remote path: %v", err)
	}
}

type uploadResult struct {
	RemotePath     string
	LocalImagePath string
	TempFile       bool
}

func uploadImage(host, remoteDir, localFile string) (*uploadResult, error) {
	if localFile != "" {
		return uploadLocalFile(host, remoteDir, localFile)
	}
	return uploadClipboardImage(host, remoteDir)
}

func uploadClipboardImage(host, remoteDir string) (*uploadResult, error) {
	clip := daemon.NewClipboardReader()
	info, err := clip.Type()
	if err != nil {
		return nil, fmt.Errorf("clipboard probe failed: %w", err)
	}
	if info.Type != daemon.ClipboardImage {
		return nil, fmt.Errorf("no image in clipboard (type: %s)", info.Type)
	}

	data, err := clip.ImageBytes()
	if err != nil {
		return nil, fmt.Errorf("clipboard image read failed: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("clipboard image is empty")
	}

	remoteHome, err := remoteHomeDir(host)
	if err != nil {
		return nil, err
	}

	remoteAbsDir := resolveRemoteDir(remoteHome, remoteDir)
	ext := imageExt(info.Format)
	filename, err := randomFilename(ext)
	if err != nil {
		return nil, err
	}
	remotePath := path.Join(remoteAbsDir, filename)

	if _, err := remoteExecNoForward(host, "mkdir -p "+shQuote(remoteAbsDir)); err != nil {
		return nil, fmt.Errorf("failed to create remote dir %s: %w", remoteAbsDir, err)
	}

	localPath, err := writeTempImage(data, ext)
	if err != nil {
		return nil, err
	}

	if err := scpUploadNoForward(host, localPath, remotePath); err != nil {
		os.Remove(localPath)
		return nil, fmt.Errorf("failed to upload image to %s: %w", remotePath, err)
	}

	return &uploadResult{
		RemotePath:     remotePath,
		LocalImagePath: localPath,
		TempFile:       true,
	}, nil
}

func uploadLocalFile(host, remoteDir, localFile string) (*uploadResult, error) {
	info, err := os.Stat(localFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read --file %s: %w", localFile, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("--file must point to an image file, got directory: %s", localFile)
	}

	remoteHome, err := remoteHomeDir(host)
	if err != nil {
		return nil, err
	}

	remoteAbsDir := resolveRemoteDir(remoteHome, remoteDir)
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(localFile)), ".")
	if ext == "" {
		ext = "png"
	}
	filename, err := randomFilename(ext)
	if err != nil {
		return nil, err
	}
	remotePath := path.Join(remoteAbsDir, filename)

	if _, err := remoteExecNoForward(host, "mkdir -p "+shQuote(remoteAbsDir)); err != nil {
		return nil, fmt.Errorf("failed to create remote dir %s: %w", remoteAbsDir, err)
	}
	if err := scpUploadNoForward(host, localFile, remotePath); err != nil {
		return nil, fmt.Errorf("failed to upload image to %s: %w", remotePath, err)
	}

	return &uploadResult{
		RemotePath:     remotePath,
		LocalImagePath: localFile,
	}, nil
}

func remoteHomeDir(host string) (string, error) {
	out, err := remoteExecNoForward(host, `sh -lc 'printf %s "$HOME"'`)
	if err != nil {
		return "", fmt.Errorf("failed to resolve remote home: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("remote home directory is empty")
	}
	return out, nil
}

func resolveRemoteDir(homeDir, remoteDir string) string {
	switch {
	case remoteDir == "~":
		return homeDir
	case strings.HasPrefix(remoteDir, "~/"):
		return path.Join(homeDir, strings.TrimPrefix(remoteDir, "~/"))
	case strings.HasPrefix(remoteDir, "/"):
		return path.Clean(remoteDir)
	default:
		return path.Join(homeDir, remoteDir)
	}
}

func imageExt(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "jpg"
	default:
		return "png"
	}
}

func randomFilename(ext string) (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("failed to generate filename suffix: %w", err)
	}
	return fmt.Sprintf("clip-%s-%s.%s", time.Now().Format("20060102-150405"), hex.EncodeToString(buf[:]), ext), nil
}

func writeTempImage(data []byte, ext string) (string, error) {
	f, err := os.CreateTemp("", "cc-clip-send-*."+ext)
	if err != nil {
		return "", fmt.Errorf("failed to create temp image: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("failed to write temp image: %w", err)
	}
	return f.Name(), nil
}

func remoteExecNoForward(host, cmd string) (string, error) {
	c := exec.Command("ssh", "-o", "ClearAllForwardings=yes", host, cmd)
	hideConsoleWindow(c)
	out, err := c.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("ssh failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func scpUploadNoForward(host, localPath, remotePath string) error {
	c := exec.Command("scp", "-o", "ClearAllForwardings=yes", localPath, fmt.Sprintf("%s:%s", host, remotePath))
	hideConsoleWindow(c)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
