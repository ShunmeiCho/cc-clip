package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
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
const sendUsage = "usage: cc-clip send [<host>] [<file>] [--file PATH] [--remote-dir DIR] [--paste] [--delay-ms N] [--no-restore]"

type sendOptions struct {
	host      string
	localFile string
	remoteDir string
	paste     bool
	delayMS   int
	noRestore bool
}

func cmdSend() {
	cfg, err := parseSendArgs(os.Args[2:], os.Stderr)
	if err != nil {
		log.Fatal(err)
	}

	host := cfg.host

	if host == "" {
		var ok bool
		var err error
		host, ok, err = defaultRemoteHost()
		if err != nil {
			log.Fatalf("cannot resolve default host: %v", err)
		}
		if !ok || host == "" {
			log.Fatal(sendUsage)
		}
	}
	restoreClipboard := !cfg.noRestore

	result, err := uploadImage(host, cfg.remoteDir, cfg.localFile)
	if err != nil {
		log.Fatalf("send failed: %v", err)
	}
	if result.TempFile {
		defer os.Remove(result.LocalImagePath)
	}

	fmt.Println(result.RemotePath)

	if !cfg.paste {
		return
	}

	if err := pasteRemotePath(result.RemotePath, result.LocalImagePath, time.Duration(cfg.delayMS)*time.Millisecond, restoreClipboard); err != nil {
		log.Fatalf("send uploaded the image but failed to inject the remote path: %v", err)
	}
}

func parseSendArgs(args []string, output io.Writer) (sendOptions, error) {
	cfg := sendOptions{
		remoteDir: defaultRemoteUploadDir,
		delayMS:   150,
	}
	flagArgs, positionalFile, err := consumeLeadingSendPositionals(&cfg, args)
	if err != nil {
		return cfg, err
	}

	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(output)

	localFile := fs.String("file", "", "upload this image file instead of reading the clipboard")
	remoteDir := fs.String("remote-dir", defaultRemoteUploadDir, "remote upload directory")
	paste := fs.Bool("paste", false, "paste the remote path into the active window")
	delayMS := fs.Int("delay-ms", 150, "delay before Ctrl+Shift+V when --paste is used")
	noRestore := fs.Bool("no-restore", false, "do not restore the original image clipboard after --paste")

	if err := fs.Parse(flagArgs); err != nil {
		return cfg, err
	}
	if *delayMS < 0 {
		return cfg, fmt.Errorf("invalid --delay-ms: %d", *delayMS)
	}

	cfg.localFile = *localFile
	cfg.remoteDir = *remoteDir
	cfg.paste = *paste
	cfg.delayMS = *delayMS
	cfg.noRestore = *noRestore

	if positionalFile != "" {
		if err := setPositionalSendFile(&cfg, positionalFile); err != nil {
			return cfg, err
		}
	}
	if err := applySendPositionals(&cfg, fs.Args()); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func consumeLeadingSendPositionals(cfg *sendOptions, args []string) ([]string, string, error) {
	leading := make([]string, 0, 2)
	flagStart := 0
	for flagStart < len(args) && !strings.HasPrefix(args[flagStart], "-") {
		leading = append(leading, args[flagStart])
		flagStart++
	}
	if len(leading) > 2 {
		return nil, "", fmt.Errorf("unexpected positional arguments %q; %s", strings.Join(leading[2:], " "), sendUsage)
	}

	switch len(leading) {
	case 0:
		return args, "", nil
	case 1:
		if looksLikeImagePath(leading[0]) {
			return args[flagStart:], leading[0], nil
		}
		cfg.host = leading[0]
		return args[flagStart:], "", nil
	default:
		cfg.host = leading[0]
		return args[flagStart:], leading[1], nil
	}
}

func applySendPositionals(cfg *sendOptions, args []string) error {
	if err := rejectFlagLikeSendPositionals(args); err != nil {
		return err
	}

	switch {
	case len(args) == 0:
		return nil
	case cfg.host != "":
		if len(args) > 1 {
			return fmt.Errorf("unexpected positional arguments %q; %s", strings.Join(args[1:], " "), sendUsage)
		}
		return setPositionalSendFile(cfg, args[0])
	case len(args) == 1:
		if looksLikeImagePath(args[0]) {
			return setPositionalSendFile(cfg, args[0])
		}
		cfg.host = args[0]
		return nil
	case len(args) == 2:
		cfg.host = args[0]
		return setPositionalSendFile(cfg, args[1])
	default:
		return fmt.Errorf("unexpected positional arguments %q; %s", strings.Join(args[2:], " "), sendUsage)
	}
}

func rejectFlagLikeSendPositionals(args []string) error {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("flag %q must appear before positional arguments; %s", arg, sendUsage)
		}
	}
	return nil
}

func setPositionalSendFile(cfg *sendOptions, file string) error {
	if cfg.localFile != "" {
		return fmt.Errorf("cannot use both --file and positional image path %q", file)
	}
	cfg.localFile = file
	return nil
}

func looksLikeImagePath(arg string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(arg)), ".")
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic":
		return true
	}
	return false
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
		return nil, fmt.Errorf("no image in clipboard (type: %s); use --file PATH or a positional image path to upload a saved image", info.Type)
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
	// Lstat (not Stat) so symlinks are rejected instead of silently followed:
	// a positional arg or --file could otherwise chase a link to a device,
	// named pipe, or unexpected target. Require a regular file so scp is
	// never asked to read a FIFO (hangs) or character device.
	info, err := os.Lstat(localFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read --file %s: %w", localFile, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("--file must point to a regular image file, got %s: %s", describeFileMode(info.Mode()), localFile)
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
	// scp's remote side is expanded by a remote shell, so remotePath must
	// be shell-quoted to resist metachars (spaces, semicolons, $(...) etc.)
	// in remoteDir or filename. "--" stops scp from interpreting a
	// leading-dash local path as an option; safeScpLocalPath additionally
	// prefixes "./" because older scp versions handle "--" inconsistently.
	safeLocal := safeScpLocalPath(localPath)
	target := fmt.Sprintf("%s:%s", host, shQuote(remotePath))
	c := exec.Command("scp", "-o", "ClearAllForwardings=yes", "--", safeLocal, target)
	hideConsoleWindow(c)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("scp failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// safeScpLocalPath defensively prefixes "./" for paths starting with "-"
// so scp never parses them as flags on versions that mishandle "--".
func safeScpLocalPath(p string) string {
	if strings.HasPrefix(p, "-") {
		return "./" + p
	}
	return p
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// describeFileMode returns a short human-readable label for non-regular
// file modes so the upload-local-file error tells the user what kind of
// path they pointed at.
func describeFileMode(m os.FileMode) string {
	switch {
	case m.IsDir():
		return "directory"
	case m&os.ModeSymlink != 0:
		return "symlink"
	case m&os.ModeNamedPipe != 0:
		return "named pipe (FIFO)"
	case m&os.ModeSocket != 0:
		return "socket"
	case m&os.ModeDevice != 0, m&os.ModeCharDevice != 0:
		return "device"
	case m&os.ModeIrregular != 0:
		return "irregular file"
	default:
		return "non-regular file"
	}
}
