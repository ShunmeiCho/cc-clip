package doctor

import (
	"strings"
	"testing"
)

func TestImageProbeCommandKeepsTokenOutOfCurlArgv(t *testing.T) {
	cmd := imageProbeCommand(18339)
	if strings.Contains(cmd, `-H "Authorization: Bearer ${TOKEN}"`) {
		t.Fatalf("image probe command leaks token through curl argv: %s", cmd)
	}
	if !strings.Contains(cmd, "curl -sf --max-time 5 -K -") {
		t.Fatalf("image probe command must pass curl auth config through stdin: %s", cmd)
	}
	if !strings.Contains(cmd, `printf 'header = "Authorization: Bearer %s"\n'`) {
		t.Fatalf("image probe command must emit Authorization header through curl config: %s", cmd)
	}
}
