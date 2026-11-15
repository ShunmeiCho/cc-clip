//go:build darwin

package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

const (
	tunnelLabelPrefix = "com.cc-clip.tunnel."
	tunnelServicePATH = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
)

type tunnelLaunchd struct {
	home string
	uid  int
	run  func(context.Context, ...string) ([]byte, error)
}

func NewTunnelManager() (TunnelManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &tunnelLaunchd{home: home, uid: os.Getuid(), run: func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	}}, nil
}

func (m *tunnelLaunchd) domain() string          { return "gui/" + strconv.Itoa(m.uid) }
func (m *tunnelLaunchd) target(id string) string { return m.domain() + "/" + tunnelLabelPrefix + id }
func (m *tunnelLaunchd) plistPath(id string) string {
	return filepath.Join(m.home, "Library", "LaunchAgents", tunnelLabelPrefix+id+".plist")
}
func (m *tunnelLaunchd) logPath(id string) string {
	return filepath.Join(m.home, "Library", "Logs", "cc-clip", "tunnels", id+".log")
}

func xmlString(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (m *tunnelLaunchd) plist(binaryPath, id string) string {
	return m.jobPlist(binaryPath, tunnelLabelPrefix+id, []string{"run", "--id", id}, true, m.logPath(id))
}

// Both the one-shot authentication check and the persistent supervisor use
// this launch policy. In particular, neither inherits the invoking shell's
// environment: launchd supplies its GUI-domain agent, if any.
func (m *tunnelLaunchd) jobPlist(binaryPath, label string, args []string, persistent bool, logPath string) string {
	var argv strings.Builder
	argv.WriteString("\n        ")
	for i, arg := range append([]string{binaryPath, "tunnel"}, args...) {
		if i == 3 {
			argv.WriteString("\n        ")
		}
		fmt.Fprintf(&argv, "<string>%s</string>", xmlString(arg))
	}
	argv.WriteString("\n    ")
	policy := "<key>KeepAlive</key><false/>"
	if persistent {
		policy = "<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n    <key>ThrottleInterval</key><integer>60</integer>"
	}
	logs := ""
	if logPath != "" {
		logs = fmt.Sprintf("<key>StandardOutPath</key><string>%s</string>\n    <key>StandardErrorPath</key><string>%s</string>", xmlString(logPath), xmlString(logPath))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key>
    <array>%s</array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key><string>%s</string>
        <key>PATH</key><string>%s</string>
    </dict>
    <key>RunAtLoad</key><true/>
    %s
    <key>ExitTimeOut</key><integer>10</integer>
    <key>Umask</key><integer>63</integer>
    %s
</dict>
</plist>
`, xmlString(label), argv.String(), xmlString(m.home), tunnelServicePATH, policy, logs)
}

func (m *tunnelLaunchd) call(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return m.run(ctx, args...)
}

func (m *tunnelLaunchd) Status(id string) (bool, error) {
	job, err := m.Inspect(id)
	return job.Loaded, err
}

func (m *tunnelLaunchd) Inspect(id string) (TunnelJob, error) {
	if err := tunnelmgr.ValidateTunnelID(id); err != nil {
		return TunnelJob{}, err
	}
	out, err := m.call("print", m.target(id))
	if err == nil {
		return parseTunnelJob(string(out)), nil
	}
	// Do not turn an unavailable GUI domain, permissions failure or timeout
	// into "job absent". launchctl reports a missing named job explicitly.
	if !errors.Is(err, context.DeadlineExceeded) && strings.Contains(string(out), "Could not find service") {
		return TunnelJob{Process: "stopped"}, nil
	}
	return TunnelJob{}, fmt.Errorf("query tunnel LaunchAgent: %s: %w", strings.TrimSpace(string(out)), err)
}

func parseTunnelJob(out string) TunnelJob {
	job := TunnelJob{Loaded: true, Process: "unknown"}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
				job.PID = pid
				job.Process = "running"
			}
		case "state":
			if value == "not running" || value == "waiting" || value == "exited" {
				job.Process = "stopped"
			}
		case "last exit code":
			if code, err := strconv.Atoi(value); err == nil {
				job.LastExit = &code
			}
		}
	}
	if job.PID > 0 {
		job.Process = "running"
	}
	return job
}

func (m *tunnelLaunchd) LogPath(id string) (string, error) {
	if err := tunnelmgr.ValidateTunnelID(id); err != nil {
		return "", err
	}
	return m.logPath(id), nil
}

func (m *tunnelLaunchd) Stop(id string) error {
	loaded, err := m.Status(id)
	if err != nil {
		return err
	}
	if !loaded {
		return nil
	}
	out, err := m.call("bootout", m.target(id))
	if err != nil {
		return fmt.Errorf("stop tunnel LaunchAgent: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (m *tunnelLaunchd) validateBinary(binaryPath string) error {
	if !filepath.IsAbs(binaryPath) || strings.ContainsAny(binaryPath+m.home, "\x00\r\n") {
		return errors.New("LaunchAgent binary and home paths must be absolute and contain no control characters")
	}
	if !filepath.IsAbs(m.home) {
		return errors.New("LaunchAgent home must be absolute")
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return fmt.Errorf("inspect LaunchAgent binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("LaunchAgent binary must be an executable regular file")
	}
	return nil
}

func (m *tunnelLaunchd) Install(binaryPath, id string) error {
	if err := tunnelmgr.ValidateTunnelID(id); err != nil {
		return err
	}
	if err := m.validateBinary(binaryPath); err != nil {
		return err
	}
	configs := tunnelmgr.NewServiceStoreAt(filepath.Join(m.home, ".cache", "cc-clip", "tunnel-specs"))
	config, err := configs.Load(id)
	if err != nil {
		return fmt.Errorf("load LaunchAgent desired configuration: %w", err)
	}
	if !config.Enabled {
		return errors.New("cannot install a disabled tunnel LaunchAgent")
	}
	loaded, err := m.Status(id)
	if err != nil {
		return err
	}
	path := m.plistPath(id)
	want := m.plist(binaryPath, id)
	existing, err := readTunnelFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	created := errors.Is(err, os.ErrNotExist)
	if err == nil && string(existing) != want {
		return fmt.Errorf("existing tunnel plist %s differs; stop and reconfigure explicitly", path)
	}
	if loaded {
		if err != nil {
			return errors.New("loaded tunnel job has no matching plist; stop it before reinstalling")
		}
		if err := privateExistingTunnelLog(m.logPath(id)); err != nil {
			return err
		}
		return nil
	}
	if err := privateTunnelDir(filepath.Dir(m.logPath(id))); err != nil {
		return err
	}
	log, err := os.OpenFile(m.logPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return fmt.Errorf("create tunnel log: %w", err)
	}
	logInfo, statErr := log.Stat()
	if statErr != nil || !privateTunnelFile(logInfo) {
		log.Close()
		return errors.New("tunnel log must be a private, user-owned regular file (0600)")
	}
	if err := log.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if created {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(want)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(path)
			return errors.Join(writeErr, closeErr)
		}
	}
	out, err := m.call("bootstrap", m.domain(), path)
	if err != nil {
		if created {
			_ = os.Remove(path)
		}
		return fmt.Errorf("start tunnel LaunchAgent: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (m *tunnelLaunchd) Uninstall(id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	// Retain desired state, runtime and logs for later diagnostics. The caller
	// owns the enabled flag; removing a job is not a configuration migration.
	path := m.plistPath(id)
	if _, err := readTunnelFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func privateTunnelDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("tunnel log directory %s must be a user-owned directory", path)
	}
	return os.Chmod(path, 0700)
}

func privateTunnelFile(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && ok && int(stat.Uid) == os.Getuid()
}

func readTunnelFile(path string) ([]byte, error) {
	f, err := tunnelmgr.OpenPrivateFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func privateExistingTunnelLog(path string) error {
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("tunnel log directory must be a private, user-owned directory (0700)")
	}
	f, err := tunnelmgr.OpenPrivateFile(path)
	if err != nil {
		return err
	}
	return f.Close()
}
