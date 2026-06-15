package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestLimitedCommandOutputRejectsOversizedOutput(t *testing.T) {
	cmd := daemonHelperCommand("write-bytes", "6")
	_, err := limitedCommandOutput(cmd, 5, "clipboard text exceeds test limit")
	if err == nil {
		t.Fatal("expected oversized command output to fail")
	}
	if !strings.Contains(err.Error(), "clipboard text exceeds test limit") {
		t.Fatalf("error = %q, want limit message", err.Error())
	}
}

func TestLimitedCommandOutputAllowsExactLimit(t *testing.T) {
	cmd := daemonHelperCommand("write-bytes", "5")
	out, err := limitedCommandOutput(cmd, 5, "clipboard text exceeds test limit")
	if err != nil {
		t.Fatalf("limitedCommandOutput returned error: %v", err)
	}
	if got := string(out); got != "xxxxx" {
		t.Fatalf("output = %q, want five bytes", got)
	}
}

func daemonHelperCommand(args ...string) *exec.Cmd {
	cmdArgs := append([]string{"-test.run=TestDaemonHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), "GO_WANT_DAEMON_HELPER_PROCESS=1")
	return cmd
}

func TestDaemonHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DAEMON_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 3 {
		os.Exit(2)
	}
	switch args[1] {
	case "write-bytes":
		n, err := strconv.Atoi(args[2])
		if err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString(strings.Repeat("x", n))
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
