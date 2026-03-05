package doctor

import (
	"fmt"
	"strings"

	"github.com/shunmei/cc-clip/internal/shim"
)

func RunRemote(host string, port int) []CheckResult {
	var results []CheckResult

	// Check SSH connectivity
	out, err := shim.RemoteExec(host, "echo ok")
	if err != nil {
		results = append(results, CheckResult{"ssh", false, fmt.Sprintf("cannot connect to %s: %v", host, err)})
		return results
	}
	if strings.TrimSpace(out) != "ok" {
		results = append(results, CheckResult{"ssh", false, fmt.Sprintf("unexpected output: %s", out)})
		return results
	}
	results = append(results, CheckResult{"ssh", true, fmt.Sprintf("connected to %s", host)})

	// Check remote binary
	out, err = shim.RemoteExec(host, "~/.local/bin/cc-clip version")
	if err != nil {
		results = append(results, CheckResult{"remote-bin", false, "cc-clip not found at ~/.local/bin/cc-clip"})
	} else {
		results = append(results, CheckResult{"remote-bin", true, strings.TrimSpace(out)})
	}

	// Check shim installation
	out, err = shim.RemoteExec(host, "head -2 ~/.local/bin/xclip 2>/dev/null || echo 'not found'")
	if err != nil || strings.Contains(out, "not found") {
		results = append(results, CheckResult{"shim", false, "xclip shim not installed"})
	} else if strings.Contains(out, "cc-clip") {
		results = append(results, CheckResult{"shim", true, "xclip shim installed"})
	} else {
		results = append(results, CheckResult{"shim", false, "~/.local/bin/xclip exists but is not cc-clip shim"})
	}

	// Check PATH priority
	out, err = shim.RemoteExec(host, "which xclip 2>/dev/null || echo 'not in PATH'")
	if err == nil && strings.Contains(out, ".local/bin") {
		results = append(results, CheckResult{"path-order", true, fmt.Sprintf("xclip resolves to %s", strings.TrimSpace(out))})
	} else {
		results = append(results, CheckResult{"path-order", false, fmt.Sprintf("xclip resolves to %s (shim not first)", strings.TrimSpace(out))})
	}

	// Check tunnel from remote side
	out, err = shim.RemoteExec(host, fmt.Sprintf(
		"bash -c 'echo >/dev/tcp/127.0.0.1/%d' 2>&1 && echo 'tunnel ok' || echo 'tunnel fail'", port))
	if strings.Contains(out, "tunnel ok") {
		results = append(results, CheckResult{"tunnel", true, fmt.Sprintf("port %d forwarded", port)})
	} else {
		results = append(results, CheckResult{"tunnel", false, fmt.Sprintf("port %d not reachable from remote", port)})
	}

	// Check token on remote
	out, err = shim.RemoteExec(host, "test -f ~/.cache/cc-clip/session.token && echo 'present' || echo 'missing'")
	if strings.Contains(out, "present") {
		results = append(results, CheckResult{"remote-token", true, "token file present"})
	} else {
		results = append(results, CheckResult{"remote-token", false, "token file missing"})
	}

	// End-to-end image round-trip (only if tunnel is up)
	if tunnelOK(results) {
		results = append(results, runImageProbe(host, port)...)
	}

	return results
}

func tunnelOK(results []CheckResult) bool {
	for _, r := range results {
		if r.Name == "tunnel" && r.OK {
			return true
		}
	}
	return false
}

func runImageProbe(host string, port int) []CheckResult {
	// Run the probe FROM the remote host through the tunnel, not from local.
	// This validates the full chain: remote -> tunnel -> daemon.
	cmd := fmt.Sprintf(
		`TOKEN=$(cat ~/.cache/cc-clip/session.token 2>/dev/null) && `+
			`curl -sf --max-time 5 `+
			`-H "Authorization: Bearer ${TOKEN}" `+
			`-H "User-Agent: cc-clip/0.1" `+
			`"http://127.0.0.1:%d/clipboard/type"`,
		port)

	out, err := shim.RemoteExec(host, cmd)
	if err != nil {
		return []CheckResult{{"image-probe", false, fmt.Sprintf("remote probe failed: %v (%s)", err, strings.TrimSpace(out))}}
	}

	out = strings.TrimSpace(out)
	if strings.Contains(out, `"type":"image"`) {
		return []CheckResult{{"image-probe", true, "clipboard has image (verified from remote)"}}
	}
	if strings.Contains(out, `"type":`) {
		return []CheckResult{{"image-probe", true, fmt.Sprintf("remote response: %s (copy an image to test)", out)}}
	}
	return []CheckResult{{"image-probe", false, fmt.Sprintf("unexpected response: %s", out)}}
}
