package daemon

import (
	"context"
	"fmt"
	"os/exec"
)

// CmuxDeliverer sends notifications via the cmux CLI tool.
// cmux detection is runtime (exec.LookPath), so no build tag is needed.
type CmuxDeliverer struct {
	path string
}

// NewCmuxDeliverer returns a CmuxDeliverer if the cmux binary is found
// on PATH, or nil if it is not available.
func NewCmuxDeliverer() *CmuxDeliverer {
	path, err := exec.LookPath("cmux")
	if err != nil {
		return nil
	}
	return &CmuxDeliverer{path: path}
}

func (d *CmuxDeliverer) Name() string { return "cmux" }

// Deliver formats the envelope and shells out to `cmux notify`.
func (d *CmuxDeliverer) Deliver(_ context.Context, env NotifyEnvelope) error {
	title, body := formatNotification(env)
	cmd := exec.Command(d.path, "notify", "--title", title, "--body", body)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cmux notify failed: %s: %w", string(out), err)
	}
	return nil
}
