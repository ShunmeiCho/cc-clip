package testshell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Command returns a bash command that runs script with HOME set to home.
func Command(home, script string) *exec.Cmd {
	name, args := commandArgs(home, script)
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	return cmd
}

func commandArgs(home, script string) (string, []string) {
	if runtime.GOOS == "windows" {
		if bash, ok := windowsBash(); ok {
			return bash, []string{"-c", withHome(msysPath(home), script)}
		}
		return "wsl.exe", []string{"--exec", "bash", "-c", withHome(wslPath(home), script)}
	}
	return "bash", []string{"-c", withHome(home, script)}
}

func windowsBash() (string, bool) {
	if p, err := exec.LookPath("bash"); err == nil && !isWindowsAppsBash(p) {
		return p, true
	}

	var candidates []string
	addRoot := func(root string, parts ...string) {
		if root != "" {
			candidates = append(candidates, filepath.Join(append([]string{root}, parts...)...))
		}
	}
	addRoot(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
	addRoot(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "bash.exe")
	addRoot(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "bash.exe")
	candidates = append(candidates, `C:\msys64\usr\bin\bash.exe`, `C:\msys2\usr\bin\bash.exe`)

	for _, p := range candidates {
		if isWindowsAppsBash(p) {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

func isWindowsAppsBash(path string) bool {
	return strings.Contains(strings.ToLower(filepath.Clean(path)), `\windowsapps\bash.exe`)
}

func withHome(home, script string) string {
	return "export HOME=" + quote(home) + "; export PATH=/usr/bin:/bin:${PATH:-}; " + script
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func wslPath(path string) string {
	path = filepath.Clean(path)
	vol := filepath.VolumeName(path)
	if len(vol) == 2 && vol[1] == ':' {
		drive := strings.ToLower(vol[:1])
		rest := strings.TrimPrefix(path[len(vol):], `\`)
		return "/mnt/" + drive + "/" + strings.ReplaceAll(rest, `\`, "/")
	}
	return strings.ReplaceAll(path, `\`, "/")
}

func msysPath(path string) string {
	path = filepath.Clean(path)
	vol := filepath.VolumeName(path)
	if len(vol) == 2 && vol[1] == ':' {
		drive := strings.ToLower(vol[:1])
		rest := strings.TrimPrefix(path[len(vol):], `\`)
		return "/" + drive + "/" + strings.ReplaceAll(rest, `\`, "/")
	}
	return strings.ReplaceAll(path, `\`, "/")
}
