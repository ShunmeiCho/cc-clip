package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/shunmei/cc-clip/internal/remoteupload"
)

const windowsProbeBegin = "__CCWINDOWS_BEGIN__"
const windowsProbeEnd = "__CCWINDOWS_END__"

// Windows commands are passed as UTF-16LE EncodedCommand. No user path,
// host, or remote-dir is interpolated into shell syntax without quoting.
func windowsEncodedCommand(script string) string {
	prelude := "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; " +
		"[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false); $OutputEncoding=[Console]::OutputEncoding; "
	units := utf16.Encode([]rune(prelude + script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		data[i*2] = byte(unit)
		data[i*2+1] = byte(unit >> 8)
	}
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(data)
}

func psLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func rawUploadSSHArgs(host, command string) []string {
	return []string{"-o", "ClearAllForwardings=yes", "-o", "LogLevel=ERROR", "--", host, command}
}

func runUploadSSH(host, command string, in io.Reader) ([]byte, error) {
	return runUploadSSHTimeout(host, command, in, 2*time.Minute)
}

func runUploadSSHTimeout(host, command string, in io.Reader, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", rawUploadSSHArgs(host, command)...)
	hideConsoleWindow(cmd)
	cmd.Stdin = in
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("SSH upload helper failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

func probeWindowsUploadHost(host string) (uploadProbe, error) {
	script := `$arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { throw 'Unsupported Windows architecture' } }; ` +
		`$p=@{protocol=1;os='windows';arch=$arch;home=$env:USERPROFILE;cache=$env:LOCALAPPDATA}; ` +
		`[Console]::Write('` + windowsProbeBegin + `'+($p | ConvertTo-Json -Compress)+'` + windowsProbeEnd + `')`
	out, err := runUploadSSH(host, windowsEncodedCommand(script), nil)
	if err != nil {
		return uploadProbe{}, err
	}
	return parseWindowsUploadProbe(string(out))
}

func parseWindowsUploadProbe(raw string) (uploadProbe, error) {
	start := strings.Index(raw, windowsProbeBegin)
	if start < 0 {
		return uploadProbe{}, fmt.Errorf("Windows probe marker missing")
	}
	rest := raw[start+len(windowsProbeBegin):]
	end := strings.Index(rest, windowsProbeEnd)
	if end < 0 {
		return uploadProbe{}, fmt.Errorf("Windows probe end marker missing")
	}
	var p uploadProbe
	if err := json.Unmarshal([]byte(rest[:end]), &p); err != nil {
		return p, err
	}
	if p.Protocol != remoteupload.ProtocolVersion || p.OS != "windows" || (p.Arch != "amd64" && p.Arch != "arm64") {
		return p, fmt.Errorf("unsupported Windows upload capabilities")
	}
	if !absoluteWindowsPath(p.Home) || !absoluteWindowsPath(p.Cache) {
		return p, fmt.Errorf("invalid Windows profile paths")
	}
	return p, nil
}

func absoluteWindowsPath(p string) bool {
	// A drive-qualified path avoids UNC/network uploads and drive-relative
	// interpretation. Profile paths containing whitespace or Unicode are valid.
	return len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) &&
		p[1] == ':' && (p[2] == '\\' || p[2] == '/') && !strings.ContainsAny(p, "\r\n\x00")
}

func formatRemotePastePath(p string) string {
	if absoluteWindowsPath(p) && strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

func ensureWindowsUploadHelper(host string, p uploadProbe) (string, error) {
	// The upload helper is new in this fork. Upstream release binaries do
	// not implement its protocol, so never download one as a fallback.
	if runtime.GOOS != "windows" || runtime.GOARCH != p.Arch {
		return "", fmt.Errorf("Windows upload currently requires a local Windows build matching the remote architecture (%s)", p.Arch)
	}
	local, err := os.Executable()
	if err != nil {
		return "", err
	}
	return ensureWindowsUploadHelperBinary(host, p, local)
}

func ensureWindowsUploadHelperBinary(host string, p uploadProbe, local string) (string, error) {
	f, err := os.Open(local)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	dir := strings.TrimRight(p.Cache, `\/`) + `\cc-clip\bin`
	helper := dir + `\cc-clip-upload-` + digest + `.exe`
	q := psLiteral(helper)
	script := `New-Item -ItemType Directory -Force -Path ` + psLiteral(dir) + ` | Out-Null; ` +
		`if (Test-Path -LiteralPath ` + q + `) { if ((Get-FileHash -LiteralPath ` + q + ` -Algorithm SHA256).Hash -ne ` + psLiteral(digest) + `) { throw 'Upload helper hash mismatch' }; [Console]::Write('present') } else { [Console]::Write('missing') }`
	out, err := runUploadSSH(host, windowsEncodedCommand(script), nil)
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(string(out)) {
	case "present":
		return helper, nil
	case "missing":
	default:
		return "", fmt.Errorf("unexpected Windows helper deployment response")
	}
	suffix, err := randomFilename("exe")
	if err != nil {
		return "", err
	}
	staging := dir + `\` + suffix
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	scp := exec.CommandContext(ctx, "scp", "-o", "ClearAllForwardings=yes", "-o", "LogLevel=ERROR", "--", local,
		host+":"+strings.ReplaceAll(staging, `\`, "/"))
	hideConsoleWindow(scp)
	if out, err := scp.CombinedOutput(); err != nil {
		return "", fmt.Errorf("deploy Windows helper using SFTP: %s: %w", out, err)
	}
	script = `try { if ((Get-FileHash -LiteralPath ` + psLiteral(staging) + ` -Algorithm SHA256).Hash -ne ` + psLiteral(digest) + `) { throw 'Upload helper checksum mismatch' }; ` +
		`if (-not (Test-Path -LiteralPath ` + q + `)) { Move-Item -LiteralPath ` + psLiteral(staging) + ` -Destination ` + q + ` } } finally { if (Test-Path -LiteralPath ` + psLiteral(staging) + `) { Remove-Item -LiteralPath ` + psLiteral(staging) + ` } }`
	if _, err := runUploadSSH(host, windowsEncodedCommand(script), nil); err != nil {
		return "", err
	}
	return helper, nil
}

func uploadWindowsFile(host, remoteDir, localFile string, p uploadProbe) (*uploadResult, error) {
	f, err := os.Open(localFile)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, remoteupload.MaxImageBytes+1))
	f.Close()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(localFile)), ".")
	header := remoteupload.Header{Protocol: remoteupload.ProtocolVersion, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Extension: ext}
	if err := header.Validate(); err != nil {
		return nil, err
	}
	helper, err := ensureWindowsUploadHelper(host, p)
	if err != nil {
		return nil, err
	}
	meta, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	input := io.MultiReader(bytes.NewReader(append(meta, '\n')), bytes.NewReader(data))
	script := `& ` + psLiteral(helper) + ` remote upload --remote-dir ` + psLiteral(remoteDir) + `; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }`
	out, err := runUploadSSH(host, windowsEncodedCommand(script), input)
	if err != nil {
		return nil, err
	}
	result, err := parseWindowsUploadResult(out, header)
	if err != nil {
		return nil, err
	}
	return &uploadResult{RemotePath: result.Path, LocalImagePath: localFile}, nil
}

func parseWindowsUploadResult(out []byte, h remoteupload.Header) (remoteupload.Result, error) {
	var result remoteupload.Result
	if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		return result, fmt.Errorf("invalid upload receipt: %w", err)
	}
	if result.Protocol != h.Protocol || result.Size != h.Size || !strings.EqualFold(result.SHA256, h.SHA256) || !absoluteWindowsPath(result.Path) {
		return result, fmt.Errorf("Windows upload receipt does not match the image")
	}
	return result, nil
}
