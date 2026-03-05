package shim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)


type Target string

const (
	TargetXclip   Target = "xclip"
	TargetWlPaste Target = "wl-paste"
	TargetAuto    Target = "auto"
)

type InstallResult struct {
	Target       Target
	ShimPath     string
	RealBinPath  string
	InstallDir   string
}

func DetectTarget() Target {
	if _, err := exec.LookPath("wl-paste"); err == nil {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return TargetWlPaste
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return TargetXclip
	}
	// Default to xclip even if not present (most common on X11 servers)
	return TargetXclip
}

func resolveTarget(target Target) Target {
	if target == TargetAuto {
		return DetectTarget()
	}
	return target
}

func findRealBinary(name string, shimDir string) (string, error) {
	absShimDir, _ := filepath.Abs(shimDir)

	// First: try `which -a` to get all resolved paths, pick the first that isn't our shim dir
	whichCmd := exec.Command("which", "-a", name)
	out, err := whichCmd.Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			resolved := strings.TrimSpace(line)
			if resolved == "" {
				continue
			}
			absResolved, _ := filepath.Abs(resolved)
			if filepath.Dir(absResolved) == absShimDir {
				continue
			}
			return resolved, nil
		}
	}

	// Fallback: manual PATH scan (e.g., `which -a` unavailable)
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if absDir == absShimDir {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("real %s binary not found in PATH", name)
}

func defaultInstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/tmp", ".local", "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

func Install(target Target, installDir string, port int) (InstallResult, error) {
	resolved := resolveTarget(target)

	if installDir == "" {
		installDir = defaultInstallDir()
	}

	if err := os.MkdirAll(installDir, 0755); err != nil {
		return InstallResult{}, fmt.Errorf("failed to create install dir %s: %w", installDir, err)
	}

	binName := string(resolved)
	shimPath := filepath.Join(installDir, binName)

	// Check if shim already installed by us
	if isOurShim(shimPath) {
		return InstallResult{}, fmt.Errorf("shim already installed at %s; run 'cc-clip uninstall' first", shimPath)
	}

	realPath, err := findRealBinary(binName, installDir)
	if err != nil {
		// No real binary found — that's OK for SSH servers without display
		realPath = fmt.Sprintf("/usr/bin/%s", binName)
	}

	var shimContent string
	switch resolved {
	case TargetXclip:
		shimContent = XclipShim(port, realPath)
	case TargetWlPaste:
		shimContent = WlPasteShim(port, realPath)
	default:
		return InstallResult{}, fmt.Errorf("unsupported target: %s", resolved)
	}

	if err := os.WriteFile(shimPath, []byte(shimContent), 0755); err != nil {
		return InstallResult{}, fmt.Errorf("failed to write shim: %w", err)
	}

	return InstallResult{
		Target:      resolved,
		ShimPath:    shimPath,
		RealBinPath: realPath,
		InstallDir:  installDir,
	}, nil
}

func Uninstall(target Target, installDir string) error {
	resolved := resolveTarget(target)

	if installDir == "" {
		installDir = defaultInstallDir()
	}

	binName := string(resolved)
	shimPath := filepath.Join(installDir, binName)

	if !isOurShim(shimPath) {
		return fmt.Errorf("%s is not a cc-clip shim (or does not exist)", shimPath)
	}

	if err := os.Remove(shimPath); err != nil {
		return fmt.Errorf("failed to remove shim: %w", err)
	}

	return nil
}

func isOurShim(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "cc-clip")
}

func CheckPathPriority(installDir string) (bool, string) {
	absInstall, err := filepath.Abs(installDir)
	if err != nil {
		return false, "cannot resolve install dir"
	}

	// Check what `which xclip` actually resolves to
	for _, binName := range []string{"xclip", "wl-paste"} {
		shimPath := filepath.Join(absInstall, binName)
		if _, err := os.Stat(shimPath); err != nil {
			continue
		}
		// Shim exists — check if `which` resolves to our shim
		whichCmd := exec.Command("which", binName)
		out, err := whichCmd.Output()
		if err != nil {
			continue
		}
		resolved := strings.TrimSpace(string(out))
		absResolved, _ := filepath.Abs(resolved)
		absShim, _ := filepath.Abs(shimPath)

		if absResolved == absShim {
			return true, fmt.Sprintf("'which %s' resolves to %s (shim)", binName, resolved)
		}
		return false, fmt.Sprintf("'which %s' resolves to %s, not %s; shim won't take priority", binName, resolved, shimPath)
	}

	return false, fmt.Sprintf("%s has no shim installed, or is not in PATH", installDir)
}
