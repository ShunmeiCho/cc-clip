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
// Honors ctx cancellation by checking early (avoids fork+exec when the
// caller already gave up) and by binding the child process to ctx via
// exec.CommandContext so a kill propagates if ctx is canceled mid-run.
func (d *CmuxDeliverer) Deliver(ctx context.Context, env NotifyEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	title, body := formatNotification(env)
	cmd := exec.CommandContext(ctx, d.path, "notify", "--title", title, "--body", body)
	if out, err := cmd.CombinedOutput(); err != nil {
		// If ctx was canceled mid-run, CommandContext kills the child
		// and returns "signal: killed" rather than context.Canceled.
		// Surface the real reason so callers can errors.Is(err, context.Canceled).
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("cmux notify failed: %s: %w", string(out), err)
	}
	return nil
}
