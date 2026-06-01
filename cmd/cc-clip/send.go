package main

import (
	"bytes"
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
	"strconv"
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

	if err := sshUploadNoForward(host, localPath, remotePath); err != nil {
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
	// named pipe, or unexpected target. Require a regular file so the
	// ssh-stdin upload is never asked to read from a FIFO (hangs) or a
	// character device.
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
	if err := sshUploadNoForward(host, localFile, remotePath); err != nil {
		return nil, fmt.Errorf("failed to upload image to %s: %w", remotePath, err)
	}

	return &uploadResult{
		RemotePath:     remotePath,
		LocalImagePath: localFile,
	}, nil
}

// Sentinel markers wrap the remote $HOME value so parseRemoteHome can extract
// it even when SSH banners (e.g. the Windows OpenSSH post-quantum warning) or a
// login shell's profile output land on the same stream. See issue #80.
const (
	remoteHomeMarkerStart = "__CCHOME_BEGIN__"
	remoteHomeMarkerEnd   = "__CCHOME_END__"
)

func remoteHomeDir(host string) (string, error) {
	out, err := remoteExecNoForward(host, remoteHomeProbeCmd())
	if err != nil {
		return "", fmt.Errorf("failed to resolve remote home: %w", err)
	}
	home, err := parseRemoteHome(out)
	if err != nil {
		return "", fmt.Errorf("failed to resolve remote home: %w", err)
	}
	return home, nil
}

// remoteHomeProbeCmd prints $HOME wrapped in sentinel markers and nothing else.
// printf emits no trailing newline, so the text between the markers is exactly
// $HOME. The markers let parseRemoteHome discard any banner lines emitted by
// SSH or the remote login shell on the same stream.
func remoteHomeProbeCmd() string {
	return "sh -lc 'printf " + remoteHomeMarkerStart + "%s" + remoteHomeMarkerEnd + ` "$HOME"'`
}

// parseRemoteHome extracts the remote $HOME from probe output, tolerating
// banner/warning lines before or after the sentinels, and validates that the
// result is a single absolute POSIX path. It fails loudly rather than return a
// polluted value that would corrupt the remote upload path (issue #80).
func parseRemoteHome(raw string) (string, error) {
	start := strings.Index(raw, remoteHomeMarkerStart)
	if start < 0 {
		return "", fmt.Errorf("remote home start marker not found in output: %q", raw)
	}
	rest := raw[start+len(remoteHomeMarkerStart):]
	end := strings.Index(rest, remoteHomeMarkerEnd)
	if end < 0 {
		return "", fmt.Errorf("remote home end marker not found in output: %q", raw)
	}
	home := strings.TrimSpace(rest[:end])
	if home == "" {
		return "", fmt.Errorf("remote home directory is empty")
	}
	if strings.ContainsAny(home, "\r\n") {
		return "", fmt.Errorf("remote home directory spans multiple lines: %q", home)
	}
	if !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("remote home directory is not an absolute path: %q", home)
	}
	return home, nil
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

// sshNoForwardArgs builds the argv for cc-clip's ssh invocations.
//
//   - ClearAllForwardings=yes stops a user's global RemoteForward in ssh_config
//     from triggering on these one-shot connections.
//   - LogLevel=ERROR suppresses connection banners such as Windows OpenSSH's
//     post-quantum key-exchange warning, which otherwise reaches the client and
//     used to pollute the remote home lookup (issue #80).
//   - "--" terminates option parsing so a host beginning with "-" can never be
//     interpreted as a flag.
func sshNoForwardArgs(host, cmd string) []string {
	return []string{
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"--", host, cmd,
	}
}

// remoteExecNoForward runs an SSH command and returns only its stdout. stderr
// is captured separately and surfaced solely in the error message, so banner
// or warning text on stderr never pollutes the returned value (issue #80).
func remoteExecNoForward(host, cmd string) (string, error) {
	c := exec.Command("ssh", sshNoForwardArgs(host, cmd)...)
	hideConsoleWindow(c)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = out
		}
		return out, fmt.Errorf("ssh failed: %s: %w", detail, err)
	}
	return out, nil
}

func sshUploadNoForward(host, localPath, remotePath string) error {
	// Stream the local file to the remote via `ssh host 'cat > <quoted>'`
	// instead of scp. OpenSSH 9.0+ defaults `scp` to the SFTP subsystem,
	// where the remote path is treated as an SFTP PATH rather than being
	// expanded by a remote shell — shell-quoting `host:'remote path'` in
	// that mode is interpreted literally and breaks uploads. Using ssh
	// redirection keeps quoting semantics stable across versions and
	// removes the leading-dash local-path hazard entirely (local path is
	// no longer an argv positional to scp).
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat local file %s: %w", localPath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("local file is empty: %s", localPath)
	}

	c := exec.Command("ssh", sshNoForwardArgs(host, sshUploadRemoteCmd(remotePath))...)
	c.Stdin = f
	hideConsoleWindow(c)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh upload failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if err := verifyRemoteUploadSize(host, remotePath, info.Size()); err != nil {
		return err
	}
	return nil
}

// sshUploadRemoteCmd returns the remote shell command used by
// sshUploadNoForward. Extracted so tests can pin the exact quoting without
// spawning a real ssh process.
//
// `umask 077` before `cat >` forces the created file to mode 0600 instead
// of the default 0644 under a 022 umask. Clipboard images can contain
// screenshots of tokens, password managers, or private chats, so they
// must not be readable by other users on the remote host.
func sshUploadRemoteCmd(remotePath string) string {
	return "umask 077; cat > " + shQuote(remotePath)
}

func verifyRemoteUploadSize(host, remotePath string, wantBytes int64) error {
	out, err := remoteExecNoForward(host, remoteUploadSizeCmd(remotePath))
	if err != nil {
		return fmt.Errorf("remote upload verification failed for %s: %w", remotePath, err)
	}
	gotBytes, err := parseRemoteUploadSize(out)
	if err != nil {
		return fmt.Errorf("remote upload verification returned invalid size for %s: %q: %w", remotePath, out, err)
	}
	if gotBytes != wantBytes {
		return fmt.Errorf("remote upload size mismatch for %s: local=%d remote=%d", remotePath, wantBytes, gotBytes)
	}
	return nil
}

func remoteUploadSizeCmd(remotePath string) string {
	q := shQuote(remotePath)
	return "sh -lc " + shQuote("test -s "+q+" && wc -c < "+q)
}

func parseRemoteUploadSize(out string) (int64, error) {
	fields := strings.Fields(out)
	for i := len(fields) - 1; i >= 0; i-- {
		if isDecimalDigits(fields[i]) {
			return strconv.ParseInt(fields[i], 10, 64)
		}
	}
	return 0, fmt.Errorf("no decimal byte count found")
}

func isDecimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
