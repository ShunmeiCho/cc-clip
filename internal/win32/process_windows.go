//go:build windows

package win32

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32DLL               = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess           = kernel32DLL.NewProc("OpenProcess")
	procCloseHandle           = kernel32DLL.NewProc("CloseHandle")
	procQueryFullProcessImage = kernel32DLL.NewProc("QueryFullProcessImageNameW")
)

const (
	processQueryLimitedInformation = 0x1000

	errorInvalidParameter = syscall.Errno(87)
)

// ErrProcessNotFound reports that no process with the requested PID exists.
// It is deliberately distinct from every other failure: callers that keep a
// PID file must be able to tell "this process is gone, forget it" apart from
// "I could not find out", and must not discard state on the latter.
var ErrProcessNotFound = errors.New("process not found")

// ProcessImageName returns the full path of the executable backing pid.
//
// This exists because the previous way of identifying a process — asking
// PowerShell for Win32_Process.CommandLine — fails in ordinary conditions:
// CommandLine comes back empty when the caller lacks rights to read it, which
// several endpoint-security configurations produce, and any transient
// PowerShell failure looks the same. Callers treated that as "not our process"
// and deleted their PID file, so `hotkey --status` reported "not running" for
// a process that was very much alive and `--stop` could no longer stop it
// (issue #140). QueryFullProcessImageNameW needs only
// PROCESS_QUERY_LIMITED_INFORMATION, spawns nothing, and cannot flash a
// console window.
//
// Returns ErrProcessNotFound when the PID does not exist. Any other error
// means the answer is unknown, not that the process is absent.
func ProcessImageName(pid int) (string, error) {
	if pid <= 0 {
		return "", ErrProcessNotFound
	}
	handle, _, callErr := procOpenProcess.Call(
		uintptr(processQueryLimitedInformation),
		0, // bInheritHandle
		uintptr(pid),
	)
	if handle == 0 {
		// OpenProcess reports a PID that no longer maps to a process as
		// ERROR_INVALID_PARAMETER. ERROR_ACCESS_DENIED and anything else mean
		// the process may well exist and we simply cannot look.
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorInvalidParameter {
			return "", ErrProcessNotFound
		}
		return "", fmt.Errorf("OpenProcess(%d): %w", pid, callErr)
	}
	defer procCloseHandle.Call(handle)

	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	r, _, callErr := procQueryFullProcessImage.Call(
		handle,
		0, // dwFlags: 0 = Win32 path format
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return "", fmt.Errorf("QueryFullProcessImageNameW(%d): %w", pid, callErr)
	}
	return syscall.UTF16ToString(buf[:size]), nil
}
