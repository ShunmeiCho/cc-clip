package xvfb

import (
	"fmt"
	"strconv"
	"strings"
)

// RemoteExecutor is the interface for running commands on a remote host.
// This mirrors the interface defined in internal/shim/pathfix.go.
// Go's implicit interface satisfaction means any type satisfying
// shim.RemoteExecutor also satisfies this interface, avoiding import cycles.
type RemoteExecutor interface {
	Exec(cmd string) (string, error)
}

// State represents the current state of a managed Xvfb instance.
type State struct {
	Display string // just the number, e.g. "42"
	PID     int
}

// ParseDisplayFile parses the content of a display file written by Xvfb -displayfd.
// It handles formats: "99", ":99", "99\n" (trailing whitespace stripped).
// Returns just the display number as a string (no colon prefix).
func ParseDisplayFile(content string) (string, error) {
	s := strings.TrimSpace(content)
	if s == "" {
		return "", fmt.Errorf("empty display file content")
	}

	// Strip leading colon if present (e.g. ":99" -> "99")
	s = strings.TrimPrefix(s, ":")

	if s == "" {
		return "", fmt.Errorf("display file contains only a colon")
	}

	// Validate that the result is a non-negative integer
	n, err := strconv.Atoi(s)
	if err != nil {
		return "", fmt.Errorf("invalid display number %q: %w", s, err)
	}
	if n < 0 {
		return "", fmt.Errorf("negative display number: %d", n)
	}

	return strconv.Itoa(n), nil
}

// SocketPath returns the X11 Unix socket path for the given display number.
func SocketPath(display string) string {
	return "/tmp/.X11-unix/X" + display
}

// IsHealthy checks whether a previously started Xvfb instance is still running
// and its X socket exists. It reads state from <stateDir>/xvfb.pid and
// <stateDir>/display.
func IsHealthy(session RemoteExecutor, stateDir string) (*State, bool) {
	pidFile := stateDir + "/xvfb.pid"
	displayFile := stateDir + "/display"

	// Read PID
	pidStr, err := session.Exec(fmt.Sprintf("cat %s 2>/dev/null", pidFile))
	if err != nil {
		return nil, false
	}
	pidStr = strings.TrimSpace(pidStr)
	if pidStr == "" {
		return nil, false
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return nil, false
	}

	// Read display number
	displayStr, err := session.Exec(fmt.Sprintf("cat %s 2>/dev/null", displayFile))
	if err != nil {
		return nil, false
	}
	display, err := ParseDisplayFile(displayStr)
	if err != nil {
		return nil, false
	}

	// Check PID is alive
	_, err = session.Exec(fmt.Sprintf("kill -0 %d 2>/dev/null", pid))
	if err != nil {
		return nil, false
	}

	// Check X socket exists
	socketPath := SocketPath(display)
	_, err = session.Exec(fmt.Sprintf("test -S %s", socketPath))
	if err != nil {
		return nil, false
	}

	return &State{Display: display, PID: pid}, true
}

// CleanStale removes stale state files (pid, display, log) from stateDir.
// It is idempotent: missing files do not cause errors.
func CleanStale(session RemoteExecutor, stateDir string) error {
	_, err := session.Exec(fmt.Sprintf(
		"rm -f %s/xvfb.pid %s/display %s/xvfb.log",
		stateDir, stateDir, stateDir,
	))
	if err != nil {
		return fmt.Errorf("failed to clean stale Xvfb state in %s: %w", stateDir, err)
	}
	return nil
}

// CheckAvailable verifies that Xvfb is installed on the remote host.
// Returns nil if available, or an error with install hints if not found.
func CheckAvailable(session RemoteExecutor) error {
	_, err := session.Exec("which Xvfb")
	if err != nil {
		return fmt.Errorf(
			"Xvfb not found. Install it:\n" +
				"  Debian/Ubuntu: sudo apt install xvfb\n" +
				"  RHEL/Fedora: sudo dnf install xorg-x11-server-Xvfb",
		)
	}
	return nil
}

// HasPasswordlessSudo checks if the remote user can run sudo without a password.
func HasPasswordlessSudo(session RemoteExecutor) bool {
	_, err := session.Exec("sudo -n true 2>/dev/null")
	return err == nil
}

// TryInstall attempts to install Xvfb on the remote host using the detected
// package manager. Requires passwordless sudo. Returns nil on success, or an
// error describing why auto-install failed.
func TryInstall(session RemoteExecutor) error {
	if !HasPasswordlessSudo(session) {
		return fmt.Errorf("passwordless sudo not available, cannot auto-install")
	}

	// Detect package manager and choose the install command.
	type pkgManager struct {
		check   string
		install string
	}
	managers := []pkgManager{
		{"which apt-get", "sudo -n apt-get install -y xvfb"},
		{"which dnf", "sudo -n dnf install -y xorg-x11-server-Xvfb"},
		{"which yum", "sudo -n yum install -y xorg-x11-server-Xvfb"},
		{"which pacman", "sudo -n pacman -S --noconfirm xorg-server-xvfb"},
	}

	for _, pm := range managers {
		if _, err := session.Exec(pm.check); err == nil {
			_, err := session.Exec(pm.install)
			if err != nil {
				return fmt.Errorf("install command failed: %w", err)
			}
			// Verify it actually installed.
			if err := CheckAvailable(session); err != nil {
				return fmt.Errorf("installed but Xvfb still not found in PATH")
			}
			return nil
		}
	}

	return fmt.Errorf("no supported package manager found (tried apt-get, dnf, yum, pacman)")
}

// StartRemote starts a headless Xvfb instance on the remote host, or reuses
// an existing healthy instance. State files are stored under stateDir.
//
// The startup sequence uses Xvfb's -displayfd option to let the server
// choose an available display number, which is written to <stateDir>/display.
func StartRemote(session RemoteExecutor, stateDir string) (*State, error) {
	// Step 1: Check Xvfb is available
	if err := CheckAvailable(session); err != nil {
		return nil, err
	}

	// Step 2: Reuse existing healthy instance
	if state, healthy := IsHealthy(session, stateDir); healthy {
		return state, nil
	}

	// Step 3: Clean stale state
	if err := CleanStale(session, stateDir); err != nil {
		return nil, err
	}

	// Step 4: Start Xvfb via nohup + -displayfd
	startScript := fmt.Sprintf(`mkdir -p %s
rm -f %s/display
nohup Xvfb -displayfd 1 -screen 0 1x1x24 -listen tcp \
  > %s/display \
  2> %s/xvfb.log \
  < /dev/null &
echo $! > %s/xvfb.pid
for i in 1 2 3 4 5; do
  [ -s %s/display ] && break
  sleep 0.2
done
cat %s/display`,
		stateDir,
		stateDir,
		stateDir, stateDir,
		stateDir,
		stateDir,
		stateDir,
	)

	displayOut, err := session.Exec(startScript)
	if err != nil {
		return nil, fmt.Errorf("failed to start Xvfb: %w", err)
	}

	// Step 5: Parse the display output
	display, err := ParseDisplayFile(displayOut)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Xvfb display output: %w", err)
	}

	// Read PID
	pidStr, err := session.Exec(fmt.Sprintf("cat %s/xvfb.pid", stateDir))
	if err != nil {
		return nil, fmt.Errorf("failed to read Xvfb PID: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil {
		return nil, fmt.Errorf("invalid Xvfb PID %q: %w", pidStr, err)
	}

	// Step 6: Verify socket exists (with brief retry)
	socketPath := SocketPath(display)
	var socketErr error
	for i := 0; i < 5; i++ {
		_, socketErr = session.Exec(fmt.Sprintf("test -S %s", socketPath))
		if socketErr == nil {
			break
		}
		session.Exec("sleep 0.2")
	}
	if socketErr != nil {
		return nil, fmt.Errorf("Xvfb socket %s not found after startup", socketPath)
	}

	return &State{Display: display, PID: pid}, nil
}

// StopRemote stops a previously started Xvfb instance on the remote host.
// It reads the PID from <stateDir>/xvfb.pid, verifies the process is
// actually Xvfb (to avoid killing an unrelated process), and sends SIGTERM
// followed by SIGKILL if necessary.
func StopRemote(session RemoteExecutor, stateDir string) error {
	pidFile := stateDir + "/xvfb.pid"

	// Step 1: Read PID
	pidStr, err := session.Exec(fmt.Sprintf("cat %s 2>/dev/null", pidFile))
	if err != nil {
		// No PID file: nothing to stop, just clean up
		return CleanStale(session, stateDir)
	}
	pidStr = strings.TrimSpace(pidStr)
	if pidStr == "" {
		return CleanStale(session, stateDir)
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		// Invalid PID file: clean up stale state
		return CleanStale(session, stateDir)
	}

	// Step 2: Verify the process is Xvfb
	comm, err := session.Exec(fmt.Sprintf("ps -p %d -o comm= 2>/dev/null", pid))
	if err != nil {
		// Process not running: just clean up
		return CleanStale(session, stateDir)
	}
	comm = strings.TrimSpace(comm)
	if comm == "" {
		// Process not running
		return CleanStale(session, stateDir)
	}

	if !strings.Contains(strings.ToLower(comm), "xvfb") {
		return fmt.Errorf(
			"PID %d is %q, not Xvfb; refusing to kill unknown process",
			pid, comm,
		)
	}

	// Step 3: SIGTERM, wait, then SIGKILL if still alive
	session.Exec(fmt.Sprintf("kill %d 2>/dev/null", pid))
	session.Exec("sleep 0.5")

	// Check if still alive
	_, err = session.Exec(fmt.Sprintf("kill -0 %d 2>/dev/null", pid))
	if err == nil {
		// Still alive: force kill
		session.Exec(fmt.Sprintf("kill -9 %d 2>/dev/null", pid))
	}

	// Step 4: Clean state files
	return CleanStale(session, stateDir)
}
