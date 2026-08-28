//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/shunmei/cc-clip/internal/win32"
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000
	wmHotkey    = 0x0312
)

const defaultHotkeyString = "alt+shift+v"

var hotkeyRunning atomic.Bool

type hotkeyBinding struct {
	modifiers uint32
	key       uint32
	display   string
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

func cmdHotkey() {
	storedCfg, hasStoredCfg, err := loadHotkeyConfig()
	if err != nil {
		log.Fatalf("hotkey config error: %v", err)
	}
	if !hasStoredCfg {
		storedCfg = hotkeyConfig{
			RemoteDir: defaultRemoteUploadDir,
			DelayMS:   150,
		}
	}

	var host string
	flagArgs := os.Args[2:]
	if len(os.Args) > 2 && !strings.HasPrefix(os.Args[2], "-") {
		host = os.Args[2]
		flagArgs = os.Args[3:]
	}

	fs := flag.NewFlagSet("hotkey", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	remoteDir := fs.String("remote-dir", storedCfg.RemoteDir, "remote upload directory")
	delayMS := fs.Int("delay-ms", storedCfg.DelayMS, "delay before Ctrl+Shift+V after the hotkey")
	hotkeyValue := fs.String("hotkey", storedCfg.Hotkey, "global hotkey to trigger remote paste (default: alt+shift+v)")
	stop := fs.Bool("stop", false, "stop the background hotkey process")
	status := fs.Bool("status", false, "show hotkey status")
	enableAutostart := fs.Bool("enable-autostart", false, "start the hotkey automatically at login")
	disableAutostart := fs.Bool("disable-autostart", false, "remove hotkey auto-start at login")
	noRestore := fs.Bool("no-restore", storedCfg.NoRestore, "leave the remote path on the clipboard instead of restoring the image after the paste keystroke")
	runLoop := fs.Bool("run-loop", false, "internal background loop")

	if err := fs.Parse(flagArgs); err != nil {
		log.Fatal(err)
	}

	if *delayMS < 0 {
		log.Fatalf("invalid --delay-ms: %d", *delayMS)
	}

	if *stop {
		stopHotkeyProcess()
		return
	}
	if *disableAutostart {
		if err := uninstallHotkeyAutostart(); err != nil {
			log.Fatalf("failed to disable hotkey auto-start: %v", err)
		}
		stopHotkeyProcess()
		fmt.Println("hotkey: auto-start disabled")
		return
	}
	if *status {
		printHotkeyStatus()
		return
	}

	if host == "" {
		host = storedCfg.Host
	}
	if host == "" {
		log.Fatal("usage: cc-clip hotkey [<host>] [--remote-dir DIR] [--hotkey KEY] [--delay-ms N] [--no-restore] [--enable-autostart] [--disable-autostart] [--stop] [--status]")
	}

	cfg := hotkeyConfig{
		Host:      host,
		RemoteDir: *remoteDir,
		DelayMS:   *delayMS,
		Hotkey:    *hotkeyValue,
		NoRestore: *noRestore,
	}
	normalizeHotkeyConfig(&cfg)
	binding, err := parseHotkey(cfg.Hotkey)
	if err != nil {
		log.Fatalf("failed to parse hotkey: %v", err)
	}
	cfg.Hotkey = binding.String()
	if err := saveHotkeyConfig(cfg); err != nil {
		log.Fatalf("failed to save hotkey config: %v", err)
	}

	if *enableAutostart {
		if err := installHotkeyAutostart(); err != nil {
			log.Fatalf("failed to enable hotkey auto-start: %v", err)
		}
		fmt.Println("hotkey: auto-start enabled")
	}

	if *runLoop {
		runHotkeyLoop(cfg)
		return
	}

	startHotkeyBackground(cfg)
}

func startHotkeyBackground(cfg hotkeyConfig) {
	hotkeyStopIfStale()
	if pid, state, reason := hotkeyProcessPID(); state == hotkeyProcessRunning {
		fmt.Printf("hotkey: already running (PID %d)\n", pid)
		return
	} else if state == hotkeyProcessUnknown {
		// Starting a second loop would fight the first one for RegisterHotKey.
		fmt.Printf("hotkey: PID %d is recorded but cannot be verified (%s); "+
			"not starting a second loop. Remove %s if it is stale.\n",
			pid, reason, hotkeyPIDPath())
		return
	}

	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("cannot determine executable path: %v", err)
	}

	args := []string{
		"hotkey",
		cfg.Host,
		"--remote-dir", cfg.RemoteDir,
		"--hotkey", cfg.Hotkey,
		"--delay-ms", strconv.Itoa(cfg.DelayMS),
		"--run-loop",
	}
	// The autostart VBS launcher runs `cc-clip hotkey --run-loop` with no other
	// arguments and relies on the saved config, so this only has to mirror what
	// an explicit foreground invocation passed.
	if cfg.NoRestore {
		args = append(args, "--no-restore")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("failed to start hotkey process: %v", err)
	}

	if err := writeHotkeyPID(cmd.Process.Pid); err != nil {
		log.Fatalf("hotkey started (PID %d) but pid file write failed: %v", cmd.Process.Pid, err)
	}
	fmt.Printf("hotkey: started in background (PID %d), trigger with %s\n", cmd.Process.Pid, cfg.Hotkey)
}

func runHotkeyLoop(cfg hotkeyConfig) {
	if err := initHotkeyLogger(); err != nil {
		log.Fatalf("hotkey logger setup failed: %v", err)
	}
	if err := writeHotkeyPID(os.Getpid()); err != nil {
		log.Fatalf("hotkey pid file write failed: %v", err)
	}
	defer os.Remove(hotkeyPIDPath())

	// Remove stale stop file only if it predates our startup. This avoids a
	// TOCTOU race where --stop writes the sentinel between VBS respawn and
	// this cleanup line, which would cause the sentinel to be deleted and the
	// VBS loop to restart us again.
	if info, err := os.Stat(hotkeyStopFilePath()); err == nil {
		if info.ModTime().Before(time.Now().Add(-2 * time.Second)) {
			os.Remove(hotkeyStopFilePath())
		}
	}

	normalizeHotkeyConfig(&cfg)
	binding, err := parseHotkey(cfg.Hotkey)
	if err != nil {
		log.Fatalf("hotkey: invalid hotkey %q: %v", cfg.Hotkey, err)
	}

	log.Printf("hotkey: starting for host=%s remote_dir=%s hotkey=%s no_restore=%t",
		cfg.Host, cfg.RemoteDir, binding.String(), cfg.NoRestore)

	// Create tray (this also calls runtime.LockOSThread)
	tray, err := newTray(cfg, binding, defaultDaemonPort())
	if err != nil {
		log.Printf("hotkey: tray creation failed (continuing without tray): %v", err)
	}

	var trayHwnd uintptr
	if tray != nil {
		if err := tray.show(); err != nil {
			log.Printf("hotkey: tray show failed: %v", err)
		} else {
			trayHwnd = tray.hwnd
			defer tray.remove()
		}
	}

	user32 := syscall.NewLazyDLL("user32.dll")
	registerHotKey := user32.NewProc("RegisterHotKey")
	unregisterHotKey := user32.NewProc("UnregisterHotKey")
	getMessage := user32.NewProc("GetMessageW")
	translateMessage := user32.NewProc("TranslateMessage")
	dispatchMessage := user32.NewProc("DispatchMessageW")

	const hotkeyID = 1
	r1, _, regErr := registerHotKey.Call(trayHwnd, hotkeyID, uintptr(binding.modifiers|modNoRepeat), uintptr(binding.key))
	if r1 == 0 {
		log.Fatalf("hotkey: RegisterHotKey failed: %v", regErr)
	}
	defer unregisterHotKey.Call(trayHwnd, hotkeyID)
	log.Printf("hotkey: registered %s", binding.String())

	var m msg
	for {
		ret, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			log.Printf("hotkey: GetMessageW returned error")
			return
		case 0:
			log.Printf("hotkey: message loop exited")
			return
		}

		// When tray is absent (trayHwnd == 0), WM_HOTKEY is posted to the
		// thread message queue and DispatchMessage won't route it anywhere.
		// Handle it explicitly here so the hotkey works in tray-less mode.
		if m.message == wmHotkey && tray == nil {
			if !hotkeyRunning.Swap(true) {
				go func() {
					defer hotkeyRunning.Store(false)
					if err := handleHotkeyPress(cfg, binding); err != nil {
						log.Printf("hotkey: send failed: %v", err)
						return
					}
					log.Printf("hotkey: send completed")
				}()
			}
			continue
		}

		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func handleHotkeyPress(cfg hotkeyConfig, binding hotkeyBinding) error {
	log.Printf("hotkey: %s pressed", binding.String())

	tray := globalTray
	if tray != nil {
		tray.showBalloon("cc-clip", "Uploading clipboard image...", niifInfo)
	}

	result, err := uploadImage(cfg.Host, cfg.RemoteDir, "")
	if err != nil {
		if tray != nil {
			if strings.Contains(err.Error(), "no image in clipboard") {
				tray.showBalloon("cc-clip", "No image in clipboard", niifWarning)
			} else {
				tray.showBalloon("cc-clip", "Send failed: "+err.Error(), niifError)
			}
		}
		return err
	}
	defer func() {
		if result.TempFile {
			os.Remove(result.LocalImagePath)
		}
	}()

	log.Printf("hotkey: uploaded %s", result.RemotePath)

	delay := time.Duration(cfg.DelayMS) * time.Millisecond
	if err := pasteRemotePath(result.RemotePath, result.LocalImagePath, delay, !cfg.NoRestore); err != nil {
		if tray != nil {
			tray.showBalloon("cc-clip", "Paste failed: "+err.Error(), niifError)
		}
		return err
	}

	if tray != nil {
		if cfg.NoRestore {
			tray.showBalloon("cc-clip", "Remote path on clipboard for "+cfg.Host, niifInfo)
		} else {
			tray.showBalloon("cc-clip", "Image pasted to "+cfg.Host, niifInfo)
		}
	}
	return nil
}

func printHotkeyStatus() {
	switch pid, state, reason := hotkeyProcessPID(); state {
	case hotkeyProcessRunning:
		fmt.Printf("hotkey: running (PID %d)\n", pid)
	case hotkeyProcessUnknown:
		// Never report this as "not running": that is a claim we cannot make,
		// and making it anyway is the bug this branch exists to prevent.
		fmt.Printf("hotkey: PID %d recorded but could not be verified (%s)\n", pid, reason)
	case hotkeyProcessOther:
		fmt.Printf("hotkey: not running (PID %d belongs to another process)\n", pid)
	default:
		fmt.Println("hotkey: not running")
	}

	if hotkeyAutostartEnabled() {
		fmt.Println("hotkey: auto-start enabled")
	} else {
		fmt.Println("hotkey: auto-start disabled")
	}

	cfg, ok, err := loadHotkeyConfig()
	if err != nil {
		fmt.Printf("hotkey: config error: %v\n", err)
		return
	}
	if !ok || cfg.Host == "" {
		fmt.Println("hotkey: no saved default host")
		return
	}

	fmt.Printf("hotkey: default host %s\n", cfg.Host)
	fmt.Printf("hotkey: remote dir %s\n", cfg.RemoteDir)
	fmt.Printf("hotkey: delay %dms\n", cfg.DelayMS)
	fmt.Printf("hotkey: key %s\n", cfg.Hotkey)
	fmt.Printf("hotkey: restore image clipboard after paste: %t\n", !cfg.NoRestore)
}

func stopHotkeyProcess() {
	// Write stop sentinel unconditionally so the VBS autostart loop exits
	// even if the hotkey process has crashed and the PID file is gone.
	// The sentinel is harmless if no VBS loop is running — it gets cleaned
	// up on the next --run-loop start.
	writeHotkeyStopFile()

	pid, state, reason := hotkeyProcessPID()
	switch state {
	case hotkeyProcessGone:
		fmt.Println("hotkey: not running (stop sentinel written)")
		return
	case hotkeyProcessUnknown:
		fmt.Printf("hotkey: cannot verify PID %d (%s); refusing to kill it. "+
			"Check it yourself and remove %s if it is stale.\n",
			pid, reason, hotkeyPIDPath())
		return
	case hotkeyProcessOther:
		fmt.Printf("hotkey: PID %d is not a cc-clip hotkey process, refusing to kill\n", pid)
		os.Remove(hotkeyPIDPath())
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("hotkey: process not found")
		os.Remove(hotkeyPIDPath())
		return
	}
	_ = proc.Kill()
	os.Remove(hotkeyPIDPath())
	fmt.Printf("hotkey: stopped PID %d\n", pid)
}

// hotkeyStopIfStale clears the PID file when it points at something that is
// definitely not our hotkey loop. An indeterminate probe leaves the file
// alone: deleting it there is how a live loop became invisible to --status
// and unkillable by --stop.
func hotkeyStopIfStale() {
	if _, state, _ := hotkeyProcessPID(); state == hotkeyProcessOther {
		os.Remove(hotkeyPIDPath())
	}
}

// hotkeyProcessState is the outcome of probing the PID recorded in the PID
// file. The distinction between "not ours" and "could not tell" is the whole
// point: only the former justifies discarding the record.
type hotkeyProcessState int

const (
	hotkeyProcessRunning hotkeyProcessState = iota // a live cc-clip hotkey loop
	hotkeyProcessGone                              // no PID file, or no such process
	hotkeyProcessOther                             // that PID belongs to something else
	hotkeyProcessUnknown                           // the process may exist; we cannot tell
)

// hotkeyProcessPID reads the recorded PID and classifies it.
//
// Identity is established from the process image path via the Win32 API, not
// from Win32_Process.CommandLine: that property comes back empty whenever the
// caller lacks rights to read it, and treating an unreadable command line as
// "not our process" is what made a running hotkey loop report itself as not
// running (issue #140). The command line is still consulted when it is
// available, to tell a hotkey loop apart from another cc-clip subcommand, but
// its absence never downgrades a positive image match.
// The third return value explains a hotkeyProcessUnknown result so callers can
// say what actually went wrong instead of "not running"; it is empty otherwise.
func hotkeyProcessPID() (int, hotkeyProcessState, string) {
	data, err := os.ReadFile(hotkeyPIDPath())
	if err != nil {
		return 0, hotkeyProcessGone, ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		_ = os.Remove(hotkeyPIDPath())
		return 0, hotkeyProcessGone, ""
	}

	image, err := win32.ProcessImageName(pid)
	switch {
	case errors.Is(err, win32.ErrProcessNotFound):
		_ = os.Remove(hotkeyPIDPath())
		return pid, hotkeyProcessGone, ""
	case err != nil:
		return pid, hotkeyProcessUnknown, err.Error()
	}

	if !strings.Contains(strings.ToLower(filepath.Base(image)), "cc-clip") {
		return pid, hotkeyProcessOther, ""
	}

	// The image is ours. Narrow it to the hotkey subcommand when the command
	// line is readable; when it is not, the image match stands on its own.
	if cmdline, cmdErr := localProcessCommand(pid); cmdErr == nil {
		if !strings.Contains(strings.ToLower(cmdline), " hotkey") {
			return pid, hotkeyProcessOther, ""
		}
	}
	return pid, hotkeyProcessRunning, ""
}

var hotkeyPIDPathOverride string

func hotkeyPIDPath() string {
	if hotkeyPIDPathOverride != "" {
		return hotkeyPIDPathOverride
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "cc-clip", "hotkey.pid")
}

func hotkeyLogPath() string {
	return hotkeyLogPathFunc()
}

var hotkeyLogPathFunc = func() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "cc-clip", "hotkey.log")
}

func writeHotkeyPID(pid int) error {
	if err := os.MkdirAll(filepath.Dir(hotkeyPIDPath()), 0755); err != nil {
		return err
	}
	return os.WriteFile(hotkeyPIDPath(), []byte(strconv.Itoa(pid)), 0644)
}

func initHotkeyLogger() error {
	if err := os.MkdirAll(filepath.Dir(hotkeyLogPath()), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(hotkeyLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
	return nil
}

func parseHotkey(value string) (hotkeyBinding, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		value = defaultHotkeyString
	}

	parts := strings.Split(value, "+")
	if len(parts) < 2 {
		return hotkeyBinding{}, fmt.Errorf("expected at least one modifier and one key, got %q", value)
	}

	var modifiers uint32
	var keyToken string
	seen := map[string]bool{}
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			return hotkeyBinding{}, fmt.Errorf("invalid hotkey %q", value)
		}
		if seen[token] {
			return hotkeyBinding{}, fmt.Errorf("duplicate hotkey token %q", token)
		}
		seen[token] = true

		switch token {
		case "alt":
			modifiers |= modAlt
		case "ctrl", "control":
			modifiers |= modControl
		case "shift":
			modifiers |= modShift
		case "win", "windows", "meta":
			modifiers |= modWin
		default:
			if keyToken != "" {
				return hotkeyBinding{}, fmt.Errorf("multiple keys in hotkey %q", value)
			}
			keyToken = token
		}
	}

	if modifiers == 0 {
		return hotkeyBinding{}, fmt.Errorf("hotkey %q must include at least one modifier", value)
	}
	if keyToken == "" {
		return hotkeyBinding{}, fmt.Errorf("hotkey %q must include a key", value)
	}

	key, displayKey, err := parseHotkeyKey(keyToken)
	if err != nil {
		return hotkeyBinding{}, err
	}

	// windowsSendCtrlShiftV synthesizes Ctrl+Shift+V; a binding with the
	// same combination would be re-caught by our own RegisterHotKey loop
	// (guarded by hotkeyRunning) and the paste would never reach the
	// terminal. Ctrl+V is the system paste shortcut and must not be
	// globally hijacked either.
	const vkV = 0x56
	if key == vkV {
		if modifiers == (modControl | modShift) {
			return hotkeyBinding{}, fmt.Errorf("hotkey %q conflicts with the simulated paste keystroke (ctrl+shift+v); choose a different combination", value)
		}
		if modifiers == modControl {
			return hotkeyBinding{}, fmt.Errorf("hotkey %q conflicts with the system paste shortcut (ctrl+v); choose a different combination", value)
		}
	}

	displayParts := make([]string, 0, 4)
	if modifiers&modControl != 0 {
		displayParts = append(displayParts, "ctrl")
	}
	if modifiers&modAlt != 0 {
		displayParts = append(displayParts, "alt")
	}
	if modifiers&modShift != 0 {
		displayParts = append(displayParts, "shift")
	}
	if modifiers&modWin != 0 {
		displayParts = append(displayParts, "win")
	}
	displayParts = append(displayParts, displayKey)

	return hotkeyBinding{
		modifiers: modifiers,
		key:       key,
		display:   strings.Join(displayParts, "+"),
	}, nil
}

func parseHotkeyKey(token string) (uint32, string, error) {
	if len(token) == 1 {
		ch := token[0]
		switch {
		case ch >= 'a' && ch <= 'z':
			return uint32(ch - 'a' + 'A'), token, nil
		case ch >= '0' && ch <= '9':
			return uint32(ch), token, nil
		}
	}

	if strings.HasPrefix(token, "f") {
		n, err := strconv.Atoi(strings.TrimPrefix(token, "f"))
		if err == nil && n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), token, nil
		}
	}

	special := map[string]struct {
		key     uint32
		display string
	}{
		"insert": {0x2D, "insert"},
		"delete": {0x2E, "delete"},
	}
	if entry, ok := special[token]; ok {
		return entry.key, entry.display, nil
	}

	return 0, "", fmt.Errorf("unsupported hotkey key %q", token)
}

func (h hotkeyBinding) String() string {
	return h.display
}
