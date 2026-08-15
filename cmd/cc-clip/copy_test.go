package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shunmei/cc-clip/internal/exitcode"
	"github.com/shunmei/cc-clip/internal/tunnel"
)

func TestRunCopySendsStdinVerbatim(t *testing.T) {
	t.Parallel()
	payload := "long command --with flags\nsecond line\n"
	var sent string
	var errOut bytes.Buffer

	code := runCopy(strings.NewReader(payload), func(s string) error {
		sent = s
		return nil
	}, &errOut)

	if code != exitcode.Success {
		t.Fatalf("exit = %d, want success; stderr:\n%s", code, errOut.String())
	}
	if sent != payload {
		t.Fatalf("sent %q, want exactly %q (no newline normalization)", sent, payload)
	}
	if !strings.Contains(errOut.String(), "copied") {
		t.Fatalf("success must be confirmed on stderr:\n%s", errOut.String())
	}
}

func TestRunCopyEmptyStdinFailsWithUsageHint(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	code := runCopy(strings.NewReader(""), func(string) error {
		t.Fatal("sender must not be called for empty stdin")
		return nil
	}, &errOut)
	if code == exitcode.Success {
		t.Fatal("empty stdin must fail")
	}
	if !strings.Contains(errOut.String(), "cc-clip copy") {
		t.Fatalf("error must show a usage example:\n%s", errOut.String())
	}
}

func TestRunCopyMapsBusinessExitCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"invalid token", tunnel.ErrTokenInvalid, exitcode.TokenInvalid},
		{"tunnel down", tunnel.ErrDaemonUnreachable, exitcode.TunnelUnreachable},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var errOut bytes.Buffer
			code := runCopy(strings.NewReader("text"), func(string) error { return tc.err }, &errOut)
			if code != tc.want {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tc.want, errOut.String())
			}
		})
	}
}

func TestRunCopyRejectsOversizedInput(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	big := strings.Repeat("a", tunnel.MaxSendTextSize()+1)
	code := runCopy(strings.NewReader(big), func(string) error {
		t.Fatal("oversized input must not be sent")
		return nil
	}, &errOut)
	if code == exitcode.Success {
		t.Fatal("oversized input must fail")
	}
	if !strings.Contains(errOut.String(), "CC_CLIP_MAX_TEXT_MB") {
		t.Fatalf("error must name the env override:\n%s", errOut.String())
	}
}
