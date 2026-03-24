//go:build windows

package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Win32 constants
const (
	wmApp          = 0x8000
	wmTray         = wmApp + 1
	wmHealthResult = wmApp + 2
	wmCommand      = 0x0111
	wmTimer   = 0x0113
	wmDestroy = 0x0002

	wmRButtonUp = 0x0205
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo    = 0x00000001
	niifWarning = 0x00000002
	niifError   = 0x00000003

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002

	hwndMessage = ^uintptr(2) // HWND_MESSAGE = (HWND)-3

	timerHealthCheck      = 1
	healthCheckIntervalMS = 30000

	menuIDTitle      = 100
	menuIDHotkey     = 101
	menuIDHost       = 102
	menuIDDaemon     = 103
	menuIDOpenLog       = 200
	menuIDOpenConfig    = 201
	menuIDToggleNotify  = 202
	menuIDQuit          = 300
)

type trayStatus int

const (
	trayStatusHealthy trayStatus = iota
	trayStatusWarning
	trayStatusError
)

type trayState struct {
	hwnd        uintptr
	icons       [3]uintptr // healthy, warning, error
	currentIcon trayStatus
	cfg         hotkeyConfig
	binding     hotkeyBinding
	daemonOK    bool
	daemonPort  int
	toastVBS    string // path to toast launcher VBS
	toastPS1    string // path to toast PowerShell script
}

// NOTIFYICONDATAW (V2 — sufficient for balloon notifications).
type notifyIconData struct {
	cbSize           uint32
	_                [4]byte // alignment padding for hWnd
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	_                [4]byte // alignment padding for hIcon
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// DLL references
var (
	user32DLL   = syscall.NewLazyDLL("user32.dll")
	shell32DLL  = syscall.NewLazyDLL("shell32.dll")
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW    = user32DLL.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32DLL.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32DLL.NewProc("DefWindowProcW")
	procDestroyWindow       = user32DLL.NewProc("DestroyWindow")
	procPostQuitMessage     = user32DLL.NewProc("PostQuitMessage")
	procSetTimer            = user32DLL.NewProc("SetTimer")
	procKillTimer           = user32DLL.NewProc("KillTimer")
	procCreatePopupMenu     = user32DLL.NewProc("CreatePopupMenu")
	procDestroyMenu         = user32DLL.NewProc("DestroyMenu")
	procAppendMenuW         = user32DLL.NewProc("AppendMenuW")
	procTrackPopupMenuEx    = user32DLL.NewProc("TrackPopupMenuEx")
	procSetForegroundWindow = user32DLL.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32DLL.NewProc("GetCursorPos")
	procPostMessageW        = user32DLL.NewProc("PostMessageW")
	procGetModuleHandleW    = kernel32DLL.NewProc("GetModuleHandleW")
	procShellNotifyIconW    = shell32DLL.NewProc("Shell_NotifyIconW")
)

var globalTray *trayState

func newTray(cfg hotkeyConfig, binding hotkeyBinding, daemonPort int) (*trayState, error) {
	runtime.LockOSThread()

	t := &trayState{
		cfg:        cfg,
		binding:    binding,
		daemonPort: daemonPort,
	}

	// Generate icons
	var err error
	t.icons[trayStatusHealthy], err = createColorIcon(0x00, 0xC0, 0x00) // green
	if err != nil {
		return nil, fmt.Errorf("create healthy icon: %w", err)
	}
	t.icons[trayStatusWarning], err = createColorIcon(0xE0, 0xB0, 0x00) // yellow
	if err != nil {
		return nil, fmt.Errorf("create warning icon: %w", err)
	}
	t.icons[trayStatusError], err = createColorIcon(0xD0, 0x00, 0x00) // red
	if err != nil {
		return nil, fmt.Errorf("create error icon: %w", err)
	}

	// Register window class
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	className := syscall.StringToUTF16Ptr("cc-clip-tray")

	wc := wndClassEx{
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     hInstance,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))

	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, fmt.Errorf("RegisterClassExW: %w", err)
	}

	// Create message-only window
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("cc-clip"))),
		0, 0, 0, 0, 0,
		hwndMessage, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowExW: %w", err)
	}
	t.hwnd = hwnd

	// Write toast helper scripts to disk
	if err := t.writeToastScripts(); err != nil {
		log.Printf("tray: toast scripts failed: %v (notifications will be disabled)", err)
	}

	globalTray = t
	return t, nil
}

func (t *trayState) show() error {
	nid := t.makeNID()
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTray
	nid.hIcon = t.icons[trayStatusHealthy]
	t.currentIcon = trayStatusHealthy
	t.setTip(&nid)

	r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		return fmt.Errorf("Shell_NotifyIconW NIM_ADD: %w", err)
	}

	// Show startup balloon
	t.showBalloon("cc-clip", fmt.Sprintf("Hotkey %s ready\nHost: %s", t.binding.String(), t.cfg.Host), niifInfo)

	// Start health check timer
	procSetTimer.Call(t.hwnd, timerHealthCheck, healthCheckIntervalMS, 0)

	// Initial health check
	go t.checkDaemonHealth()

	return nil
}

func (t *trayState) remove() {
	procKillTimer.Call(t.hwnd, timerHealthCheck)
	nid := t.makeNID()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	procDestroyWindow.Call(t.hwnd)
	for _, icon := range t.icons {
		if icon != 0 {
			destroyIcon(icon)
		}
	}
}

func (t *trayState) setStatus(s trayStatus) {
	if s == t.currentIcon {
		return
	}
	t.currentIcon = s
	nid := t.makeNID()
	nid.uFlags = nifIcon | nifTip
	nid.hIcon = t.icons[s]
	t.setTip(&nid)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (t *trayState) showBalloon(title, msg string, _ uint32) {
	if t.toastVBS == "" || !t.cfg.notificationsEnabled() {
		return
	}
	// wscript.exe is a GUI subsystem process — no console window flash.
	cmd := exec.Command("wscript.exe", "//nologo", "//B", t.toastVBS, title, msg)
	if err := cmd.Start(); err != nil {
		log.Printf("tray: toast failed: %v", err)
		return
	}
	go cmd.Wait()
}

func (t *trayState) writeToastScripts() error {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(cacheDir, "cc-clip")
	os.MkdirAll(dir, 0755)

	t.toastPS1 = filepath.Join(dir, "toast.ps1")
	t.toastVBS = filepath.Join(dir, "toast.vbs")

	ps1Content := `param([string]$Title, [string]$Message)
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] | Out-Null
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$et = [System.Security.SecurityElement]::Escape($Title)
$em = [System.Security.SecurityElement]::Escape($Message)
$x.LoadXml("<toast duration=""short""><visual><binding template=""ToastText02""><text id=""1"">$et</text><text id=""2"">$em</text></binding></visual><audio silent=""true""/></toast>")
$t = New-Object Windows.UI.Notifications.ToastNotification $x
$t.Tag = "status"
$t.Group = "cc-clip"
$t.ExpirationTime = [System.DateTimeOffset]::Now.AddSeconds(30)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("cc-clip").Show($t)
`
	if err := os.WriteFile(t.toastPS1, []byte(ps1Content), 0644); err != nil {
		return err
	}

	// VBS launcher: runs PowerShell completely hidden (window style 0).
	// wscript.exe itself is a GUI app so no console window is created.
	vbsContent := fmt.Sprintf(`Set shell = CreateObject("WScript.Shell")
title = WScript.Arguments(0)
msg = WScript.Arguments(1)
cmd = "powershell.exe -WindowStyle Hidden -NoProfile -ExecutionPolicy Bypass -File ""%s"" -Title """ & title & """ -Message """ & msg & """"
shell.Run cmd, 0, False
`, strings.ReplaceAll(t.toastPS1, `\`, `\\`))

	return os.WriteFile(t.toastVBS, []byte(vbsContent), 0644)
}

func (t *trayState) setTip(nid *notifyIconData) {
	status := "healthy"
	if !t.daemonOK {
		status = "daemon not reachable"
	}
	tip := fmt.Sprintf("cc-clip | %s | %s", t.binding.String(), status)
	if len(tip) > 127 {
		tip = tip[:127]
	}
	copyUTF16(nid.szTip[:], tip)
}

func (t *trayState) makeNID() notifyIconData {
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = t.hwnd
	nid.uID = 1
	return nid
}

func (t *trayState) showContextMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	versionStr := fmt.Sprintf("cc-clip %s", version)
	appendMenuItem(hMenu, mfString|mfGrayed, menuIDTitle, versionStr)
	appendMenuItem(hMenu, mfSeparator, 0, "")
	appendMenuItem(hMenu, mfString|mfGrayed, menuIDHotkey, fmt.Sprintf("Hotkey: %s", t.binding.String()))
	appendMenuItem(hMenu, mfString|mfGrayed, menuIDHost, fmt.Sprintf("Host: %s", t.cfg.Host))

	daemonStatus := "Daemon: running"
	if !t.daemonOK {
		daemonStatus = "Daemon: not reachable"
	}
	appendMenuItem(hMenu, mfString|mfGrayed, menuIDDaemon, daemonStatus)
	appendMenuItem(hMenu, mfSeparator, 0, "")
	appendMenuItem(hMenu, mfString, menuIDOpenLog, "Open Log")
	appendMenuItem(hMenu, mfString, menuIDOpenConfig, "Open Config Folder")
	if t.cfg.notificationsEnabled() {
		appendMenuItem(hMenu, mfString, menuIDToggleNotify, "Mute Notifications")
	} else {
		appendMenuItem(hMenu, mfString, menuIDToggleNotify, "Enable Notifications")
	}
	appendMenuItem(hMenu, mfSeparator, 0, "")
	appendMenuItem(hMenu, mfString, menuIDQuit, "Quit")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(t.hwnd)
	procTrackPopupMenuEx.Call(hMenu, tpmRightButton, uintptr(pt.x), uintptr(pt.y), t.hwnd, 0)
	// Send WM_NULL to dismiss menu
	procPostMessageW.Call(t.hwnd, 0, 0, 0)
}

func (t *trayState) handleMenuCommand(id uint16) {
	switch id {
	case menuIDOpenLog:
		logPath := hotkeyLogPath()
		exec.Command("notepad.exe", logPath).Start()
	case menuIDOpenConfig:
		configPath := hotkeyConfigPath()
		exec.Command("explorer.exe", filepath.Dir(configPath)).Start()
	case menuIDToggleNotify:
		enabled := t.cfg.notificationsEnabled()
		v := !enabled
		t.cfg.Notifications = &v
		if err := saveHotkeyConfig(t.cfg); err != nil {
			log.Printf("tray: failed to save notification setting: %v", err)
		} else if v {
			t.showBalloon("cc-clip", "Notifications enabled", niifInfo)
		}
	case menuIDQuit:
		t.quit()
	}
}

func (t *trayState) quit() {
	// Write stop file so VBScript auto-restart loop exits
	writeHotkeyStopFile()
	// Clean up tray
	t.remove()
	// Remove PID file
	os.Remove(hotkeyPIDPath())
	procPostQuitMessage.Call(0)
}

func (t *trayState) checkDaemonHealth() {
	addr := fmt.Sprintf("127.0.0.1:%d", t.daemonPort)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	var ok uintptr
	if err != nil {
		log.Printf("tray: health check failed: %v", err)
	} else {
		conn.Close()
		ok = 1
	}
	// Post result to main thread — Win32 icon updates must happen there.
	procPostMessageW.Call(t.hwnd, wmHealthResult, ok, 0)
}

func trayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	t := globalTray
	if t == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return ret
	}

	switch msg {
	case wmHotkey:
		if hotkeyRunning.Swap(true) {
			log.Printf("hotkey: ignored repeated %s while previous send is still running", t.binding.String())
			return 0
		}
		go func() {
			defer hotkeyRunning.Store(false)
			if err := handleHotkeyPress(t.cfg.Host, t.cfg.RemoteDir, t.binding, time.Duration(t.cfg.DelayMS)*time.Millisecond); err != nil {
				log.Printf("hotkey: send failed: %v", err)
				return
			}
			log.Printf("hotkey: send completed")
		}()
		return 0

	case wmTray:
		switch lParam {
		case wmRButtonUp:
			t.showContextMenu()
		}
		return 0

	case wmCommand:
		t.handleMenuCommand(uint16(wParam & 0xFFFF))
		return 0

	case wmHealthResult:
		if wParam != 0 {
			t.daemonOK = true
			t.setStatus(trayStatusHealthy)
		} else {
			t.daemonOK = false
			t.setStatus(trayStatusWarning)
		}
		return 0

	case wmTimer:
		if wParam == timerHealthCheck {
			go t.checkDaemonHealth()
		}
		return 0

	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

// appendMenuItem adds a menu item to a popup menu.
func appendMenuItem(hMenu uintptr, flags uint32, id uint32, text string) {
	textPtr := syscall.StringToUTF16Ptr(text)
	procAppendMenuW.Call(hMenu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(textPtr)))
}

// copyUTF16 copies a Go string into a UTF-16 buffer.
func copyUTF16(dst []uint16, src string) {
	utf16 := syscall.StringToUTF16(src)
	n := len(dst) - 1
	if len(utf16) < n {
		n = len(utf16)
	}
	copy(dst[:n], utf16[:n])
	dst[n] = 0
}

var hotkeyStopFilePathOverride string

// hotkeyStopFilePath returns the path to the stop sentinel file.
func hotkeyStopFilePath() string {
	if hotkeyStopFilePathOverride != "" {
		return hotkeyStopFilePathOverride
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "cc-clip", "hotkey.stop")
}

// writeHotkeyStopFile writes the sentinel file that prevents VBScript restart.
func writeHotkeyStopFile() {
	path := hotkeyStopFilePath()
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("stop"), 0644)
}

func defaultDaemonPort() int {
	p := os.Getenv("CC_CLIP_PORT")
	if p != "" {
		var port int
		if _, err := fmt.Sscanf(p, "%d", &port); err == nil && port > 0 {
			return port
		}
	}
	return 18339
}
