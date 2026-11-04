//go:build windows

package winbridge

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/shunmei/cc-clip/internal/daemon"
)

var user = syscall.NewLazyDLL("user32.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var registerClass = user.NewProc("RegisterClassExW")
var createWindow = user.NewProc("CreateWindowExW")
var defWindowProc = user.NewProc("DefWindowProcW")
var getMessage = user.NewProc("GetMessageW")
var dispatchMessage = user.NewProc("DispatchMessageW")
var postMessage = user.NewProc("PostMessageW")
var destroyWindow = user.NewProc("DestroyWindow")
var unregisterClass = user.NewProc("UnregisterClassW")
var openClipboard = user.NewProc("OpenClipboard")
var closeClipboard = user.NewProc("CloseClipboard")
var emptyClipboard = user.NewProc("EmptyClipboard")
var setClipboard = user.NewProc("SetClipboardData")
var getOwner = user.NewProc("GetClipboardOwner")
var registerFormat = user.NewProc("RegisterClipboardFormatW")
var globalAlloc = kernel.NewProc("GlobalAlloc")
var globalLock = kernel.NewProc("GlobalLock")
var globalUnlock = kernel.NewProc("GlobalUnlock")
var globalFree = kernel.NewProc("GlobalFree")
var copyMemory = kernel.NewProc("RtlMoveMemory")
var setLastError = kernel.NewProc("SetLastError")
var owners sync.Map
var classSequence atomic.Uint64
var windowCallback = syscall.NewCallback(windowProc)

const wmUpdate = 0x8001
const wmRenderFormat = 0x305
const wmRenderAll = 0x306
const wmClose = 0x10
const cfDIBV5 = 17

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}
type owner struct {
	ctx             context.Context
	source          Source
	hwnd, pngFormat uintptr
	mu              sync.Mutex
	desired         daemon.ClipboardInfo
	available       bool
	last            daemon.ClipboardInfo
	png, dib        []byte
	cachedRevision  uint32
	failure         error
}

func Station() (map[string]any, error) {
	h, _, err := user.NewProc("GetProcessWindowStation").Call()
	if h == 0 {
		return nil, fmt.Errorf("GetProcessWindowStation: %v", err)
	}
	name := make([]uint16, 512)
	var needed uint32
	r, _, err := user.NewProc("GetUserObjectInformationW").Call(h, 2, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return nil, fmt.Errorf("GetUserObjectInformation: %v", err)
	}
	var session uint32
	r, _, err = kernel.NewProc("ProcessIdToSessionId").Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&session)))
	if r == 0 {
		return nil, fmt.Errorf("ProcessIdToSessionId: %v", err)
	}
	return map[string]any{"window_station": syscall.UTF16ToString(name), "session_id": session, "pid": os.Getpid()}, nil
}

// Run owns a message-only window in this SSH logon's window station. Image
// formats use delayed rendering: no screenshot bytes cross SSH until a native
// clipboard consumer requests them. Only this window's clipboard is cleared.
func Run(ctx context.Context, source Source, ready chan<- error) (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	sentReady := false
	defer func() {
		if !sentReady {
			ready <- err
		}
	}()
	info, err := source.Type(ctx)
	if err != nil {
		return fmt.Errorf("clipboard source unavailable: %w", err)
	}
	if info.Type == daemon.ClipboardImage && info.Revision == 0 {
		return fmt.Errorf("local daemon has no Windows clipboard revision; run the current fork build")
	}
	name := syscall.StringToUTF16Ptr(fmt.Sprintf("cc-clip-bridge-%d-%d", os.Getpid(), classSequence.Add(1)))
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	wc := windowClass{Proc: windowCallback, Instance: instance, Name: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	r, _, e := registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	if r == 0 {
		return fmt.Errorf("RegisterClassExW: %v", e)
	}
	defer unregisterClass.Call(uintptr(unsafe.Pointer(name)), instance)
	hwnd, _, e := createWindow.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW in SSH session: %v", e)
	}
	o := &owner{ctx: ctx, source: source, hwnd: hwnd, desired: info, available: true}
	o.pngFormat, _, e = registerFormat.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("PNG"))))
	if o.pngFormat == 0 {
		destroyWindow.Call(hwnd)
		return fmt.Errorf("RegisterClipboardFormatW: %v", e)
	}
	owners.Store(hwnd, o)
	defer owners.Delete(hwnd)
	defer func() { o.clear(); destroyWindow.Call(hwnd) }()
	if err := o.update(); err != nil {
		return err
	}
	ready <- nil
	sentReady = true
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go o.poll(loopCtx)
	go func() { <-loopCtx.Done(); postMessage.Call(hwnd, wmClose, 0, 0) }()
	var msg message
	for {
		r, _, e := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("GetMessageW: %v", e)
		}
		if r == 0 {
			o.mu.Lock()
			failure := o.failure
			o.mu.Unlock()
			return failure
		}
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (o *owner) poll(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var unavailableSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := o.source.Type(ctx)
			available := err == nil && (info.Type != daemon.ClipboardImage || info.Revision != 0)
			if available {
				unavailableSince = time.Time{}
			} else if unavailableSince.IsZero() {
				unavailableSince = time.Now()
			}
			o.mu.Lock()
			o.desired, o.available = info, available
			o.mu.Unlock()
			postMessage.Call(o.hwnd, wmUpdate, 0, 0)
			if !unavailableSince.IsZero() && time.Since(unavailableSince) >= 10*time.Second {
				o.mu.Lock()
				o.failure = fmt.Errorf("clipboard tunnel unavailable for 10 seconds; stopping bridge and agent")
				o.mu.Unlock()
				postMessage.Call(o.hwnd, wmClose, 0, 0)
				return
			}
		}
	}
}

func (o *owner) clear() {
	h, _, _ := getOwner.Call()
	if h != o.hwnd {
		return
	}
	if r, _, _ := openClipboard.Call(o.hwnd); r != 0 {
		emptyClipboard.Call()
		closeClipboard.Call()
	}
	o.png, o.dib = nil, nil
}

func (o *owner) update() error {
	o.mu.Lock()
	info, available := o.desired, o.available
	o.mu.Unlock()
	if !available || info.Type != daemon.ClipboardImage {
		o.clear()
		o.last = daemon.ClipboardInfo{}
		return nil
	}
	if info == o.last {
		return nil
	}
	r, _, e := openClipboard.Call(o.hwnd)
	if r == 0 {
		return fmt.Errorf("OpenClipboard in SSH session: %v", e)
	}
	defer closeClipboard.Call()
	if r, _, e := emptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", e)
	}
	o.png, o.dib = nil, nil
	for _, format := range []uintptr{o.pngFormat, cfDIBV5} {
		setLastError.Call(0)
		r, _, e := setClipboard.Call(format, 0)
		if r == 0 && e != syscall.Errno(0) {
			return fmt.Errorf("advertise clipboard image format: %v", e)
		}
	}
	o.last = info
	return nil
}

func (o *owner) render(format uintptr) error {
	info, err := o.source.Type(o.ctx)
	if err != nil || info.Type != daemon.ClipboardImage || info.Revision == 0 {
		return fmt.Errorf("clipboard image no longer available")
	}
	if o.png == nil || info.Revision != o.cachedRevision {
		data, err := o.source.Image(o.ctx)
		if err != nil {
			return err
		}
		after, err := o.source.Type(o.ctx)
		if err != nil || after != info {
			return fmt.Errorf("local clipboard changed while fetching image; retry paste")
		}
		pngData, dib, err := Formats(data)
		if err != nil {
			return err
		}
		o.png, o.dib, o.cachedRevision = pngData, dib, info.Revision
	}
	data := o.dib
	if format == o.pngFormat {
		data = o.png
	} else if format != cfDIBV5 {
		return fmt.Errorf("unsupported clipboard format")
	}
	return setGlobalData(format, data)
}

func setGlobalData(format uintptr, data []byte) error {
	h, _, e := globalAlloc.Call(2, uintptr(len(data))) // GMEM_MOVEABLE
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %v", e)
	}
	p, _, e := globalLock.Call(h)
	if p == 0 {
		globalFree.Call(h)
		return fmt.Errorf("GlobalLock: %v", e)
	}
	copyMemory.Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	runtime.KeepAlive(data)
	globalUnlock.Call(h)
	if r, _, e := setClipboard.Call(format, h); r == 0 {
		globalFree.Call(h)
		return fmt.Errorf("SetClipboardData: %v", e)
	}
	return nil // Windows owns h after successful SetClipboardData.
}

func windowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	value, ok := owners.Load(hwnd)
	if !ok {
		r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
		return r
	}
	o := value.(*owner)
	switch msg {
	case wmUpdate:
		if err := o.update(); err != nil {
			log.Printf("windows-bridge: %v", err)
		}
		return 0
	case wmRenderFormat:
		// The requesting application holds the clipboard open. Opening it
		// here would deadlock/fail; SetClipboardData is the documented path.
		if err := o.render(wparam); err != nil {
			o.png, o.dib = nil, nil
			log.Printf("windows-bridge: image read failed: %v", err)
		}
		return 0
	case wmRenderAll:
		// Exit deliberately drops formats rather than retaining remote data.
		return 0
	case wmClose:
		o.clear()
		user.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}
