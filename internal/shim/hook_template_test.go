package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookTemplateUsesNotificationNonceAndHealthLog(t *testing.T) {
	got := HookScript(18339)
	for _, needle := range []string{
		"notify.nonce",
		"notify-health.log",
		"application/x-claude-hook",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected template to contain %q", needle)
		}
	}
}

func TestHookScriptSubstitutesPort(t *testing.T) {
	got := HookScript(19999)
	if !strings.Contains(got, "19999") {
		t.Fatal("expected template to contain the substituted port 19999")
	}
	// The default port line should have the substituted value, not a format directive.
	// Note: %d also appears in date formats (%Y-%m-%dT) which is expected.
	if strings.Contains(got, "${CC_CLIP_PORT:-%"+"d}") {
		t.Fatal("template still contains unsubstituted port format directive")
	}
}

func TestHookScriptDoesNotUseSessionToken(t *testing.T) {
	got := HookScript(18339)
	if strings.Contains(got, "session.token") {
		t.Fatal("hook template must use notify.nonce, not session.token")
	}
}

func TestHookScriptKeepsNonceOutOfCurlArgv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmpDir := t.TempDir()
	argvLog, stdinLog := writeFakeCurl(t, tmpDir)
	home := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".cache", "cc-clip"), 0700); err != nil {
		t.Fatalf("mkdir home cache: %v", err)
	}
	nonce := "notify-nonce-must-not-appear-in-argv"
	if err := os.WriteFile(filepath.Join(home, ".cache", "cc-clip", "notify.nonce"), []byte(nonce+"\n"), 0600); err != nil {
		t.Fatalf("write nonce: %v", err)
	}

	hookPath := filepath.Join(tmpDir, "cc-clip-hook")
	if err := os.WriteFile(hookPath, []byte(HookScript(18339)), 0755); err != nil {
		t.Fatalf("write hook script: %v", err)
	}

	cmd := exec.Command("bash", hookPath)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop"}`)
	cmd.Env = append(os.Environ(),
		"PATH="+tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"CC_CLIP_HOST_ALIAS=testhost",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook execution failed: %v output=%q", err, string(out))
	}

	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read fake curl argv log: %v", err)
	}
	if strings.Contains(string(argv), nonce) {
		t.Fatalf("nonce leaked through curl argv: %q", string(argv))
	}

	stdinConfig, err := os.ReadFile(stdinLog)
	if err != nil {
		t.Fatalf("read fake curl stdin config: %v", err)
	}
	if !strings.Contains(string(stdinConfig), nonce) {
		t.Fatalf("expected nonce in curl stdin config, got %q", string(stdinConfig))
	}
}

func TestHookScriptKeepsPayloadOutOfCurlArgv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmpDir := t.TempDir()
	argvLog, _ := writeFakeCurl(t, tmpDir)
	dataLog := filepath.Join(tmpDir, "curl-data.log")
	home := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".cache", "cc-clip"), 0700); err != nil {
		t.Fatalf("mkdir home cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cache", "cc-clip", "notify.nonce"), []byte("nonce\n"), 0600); err != nil {
		t.Fatalf("write nonce: %v", err)
	}

	hookPath := filepath.Join(tmpDir, "cc-clip-hook")
	if err := os.WriteFile(hookPath, []byte(HookScript(18339)), 0755); err != nil {
		t.Fatalf("write hook script: %v", err)
	}

	secretPayload := `{"hook_event_name":"Stop","last_assistant_message":"secret-hook-payload-must-not-appear-in-argv"}`
	cmd := exec.Command("bash", hookPath)
	cmd.Stdin = strings.NewReader(secretPayload)
	cmd.Env = append(os.Environ(),
		"PATH="+tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"CC_CLIP_HOST_ALIAS=testhost",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook execution failed: %v output=%q", err, string(out))
	}

	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read fake curl argv log: %v", err)
	}
	if strings.Contains(string(argv), "secret-hook-payload-must-not-appear-in-argv") {
		t.Fatalf("hook payload leaked through curl argv: %q", string(argv))
	}

	data, err := os.ReadFile(dataLog)
	if err != nil {
		t.Fatalf("read fake curl data log: %v", err)
	}
	if !strings.Contains(string(data), "secret-hook-payload-must-not-appear-in-argv") {
		t.Fatalf("expected hook payload to be sent as curl data, got %q", string(data))
	}
}

func TestHookScriptDoesNotInterpolateHostIntoPythonSource(t *testing.T) {
	got := HookScript(18339)
	// The host alias must be passed to python3 via the environment, never
	// expanded by bash directly into the python -c source. An interpolated
	// single-quoted literal like d['_cc_clip_host'] = '${_CC_CLIP_HOST_ALIAS}'
	// is an injection-shaped construct: a value containing a single quote
	// would break out of the literal and execute attacker-controlled python.
	if strings.Contains(got, "${_CC_CLIP_HOST_ALIAS}") {
		t.Fatal("host alias must not be bash-expanded into the python -c source; pass it via the environment instead")
	}
	// The python source must read the host from the environment.
	if !strings.Contains(got, "os.environ") {
		t.Fatal("expected python source to read the host alias from os.environ")
	}
}

func TestHookScriptHandlesHostAliasWithSingleQuoteSafely(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tmpDir := t.TempDir()
	argvLog, _ := writeFakeCurl(t, tmpDir)
	dataLog := filepath.Join(tmpDir, "curl-data.log")
	home := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".cache", "cc-clip"), 0700); err != nil {
		t.Fatalf("mkdir home cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cache", "cc-clip", "notify.nonce"), []byte("nonce\n"), 0600); err != nil {
		t.Fatalf("write nonce: %v", err)
	}

	hookPath := filepath.Join(tmpDir, "cc-clip-hook")
	if err := os.WriteFile(hookPath, []byte(HookScript(18339)), 0755); err != nil {
		t.Fatalf("write hook script: %v", err)
	}

	// A malicious host alias containing a single quote. If the value were
	// interpolated into single-quoted python source, this would either
	// crash the python interpreter (SyntaxError) or execute injected code.
	// Passed via env, it must be embedded verbatim into the JSON payload.
	// The marker path is absolute so the assertion is robust regardless of
	// the python interpreter's working directory.
	pwnedMarker := filepath.Join(tmpDir, "pwned")
	maliciousHost := "evil'; import os; os.system('touch " + pwnedMarker + "'); x='"

	cmd := exec.Command("bash", hookPath)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop"}`)
	cmd.Env = append(os.Environ(),
		"PATH="+tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"CC_CLIP_HOST_ALIAS="+maliciousHost,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook execution failed: %v output=%q", err, string(out))
	}

	// No injected command must have run.
	if _, err := os.Stat(pwnedMarker); err == nil {
		t.Fatal("python injection executed: marker file was created")
	}

	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read fake curl argv: %v", err)
	}
	if strings.Contains(string(argv), "import os") {
		t.Fatalf("host alias leaked through curl argv: %q", string(argv))
	}

	// The payload forwarded to curl must carry the host alias verbatim as JSON,
	// proving the value was treated as data, not code.
	body, err := os.ReadFile(dataLog)
	if err != nil {
		t.Fatalf("read fake curl data: %v", err)
	}
	if !strings.Contains(string(body), `_cc_clip_host`) {
		t.Fatalf("expected _cc_clip_host key in forwarded payload, got %q", string(body))
	}
	// The single quote and the literal text must be JSON-escaped/encoded as
	// a string value, not interpreted. The substring "import os" appears in
	// the alias text and should be present verbatim as data within the JSON.
	if !strings.Contains(string(body), "import os") {
		t.Fatalf("expected the malicious alias to be preserved as JSON data, got %q", string(body))
	}
}

func TestHookScriptAlwaysExitsZero(t *testing.T) {
	got := HookScript(18339)
	if !strings.Contains(got, "exit 0") {
		t.Fatal("hook script must always exit 0 to avoid blocking Claude Code")
	}
}

func TestHookScriptIsValidBash(t *testing.T) {
	got := HookScript(18339)
	if !strings.HasPrefix(got, "#!/usr/bin/env bash") {
		t.Fatal("hook script must start with bash shebang")
	}
	if !strings.Contains(got, "set -euo pipefail") {
		t.Fatal("hook script must use strict mode")
	}
}
