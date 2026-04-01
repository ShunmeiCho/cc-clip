package daemon

import (
	"context"
	"fmt"
	"log"
)

// Deliverer sends a notification envelope through a specific transport.
// Implementations must be safe for concurrent use.
type Deliverer interface {
	Deliver(ctx context.Context, env NotifyEnvelope) error
	Name() string
}

// DeliveryChain tries adapters in priority order, falling through on failure.
// The first successful delivery stops the chain.
type DeliveryChain struct {
	adapters []Deliverer
}

// Deliver iterates through adapters in order. Returns nil on the first
// success. If all adapters fail, returns the last error. If no adapters
// are configured, returns an error.
func (c *DeliveryChain) Deliver(ctx context.Context, env NotifyEnvelope) error {
	var lastErr error
	for _, adapter := range c.adapters {
		if err := adapter.Deliver(ctx, env); err != nil {
			lastErr = err
			log.Printf("delivery adapter %s failed: %v", adapter.Name(), err)
			continue
		}
		return nil
	}
	if lastErr == nil {
		return fmt.Errorf("no delivery adapters configured")
	}
	return lastErr
}

// Notify satisfies the Notifier interface by bridging NotifyEvent into
// NotifyEnvelope via newImageTransferEnvelope, then delegating to Deliver.
// This allows DeliveryChain to be used as a drop-in Notifier replacement.
func (c *DeliveryChain) Notify(ctx context.Context, evt NotifyEvent) error {
	env := newImageTransferEnvelope("clipboard", ImageTransferPayload{
		SessionID:   evt.SessionID,
		Seq:         evt.Seq,
		Fingerprint: evt.Fingerprint,
		ImageData:   evt.ImageData,
		Format:      evt.Format,
		Width:       evt.Width,
		Height:      evt.Height,
		DuplicateOf: evt.DuplicateOf,
	})
	return c.Deliver(ctx, env)
}

// BuildDeliveryChain constructs the default chain with available adapters.
// cmux is tried first (cross-platform tmux notification), then the
// platform-specific deliverer (macOS terminal-notifier / osascript).
// platformDeliverer() is defined per-platform in deliver_other.go / notify_darwin.go.
func BuildDeliveryChain() *DeliveryChain {
	adapters := make([]Deliverer, 0, 2)
	if cmux := NewCmuxDeliverer(); cmux != nil {
		adapters = append(adapters, cmux)
	}
	if d := platformDeliverer(); d != nil {
		adapters = append(adapters, d)
	}
	return &DeliveryChain{adapters: adapters}
}

// formatNotification extracts display-ready title and body text from any
// envelope kind. Used by both cmux and darwin adapters.
func formatNotification(env NotifyEnvelope) (title, body string) {
	switch env.Kind {
	case KindImageTransfer:
		if env.ImageTransfer != nil {
			title = fmt.Sprintf("cc-clip #%d", env.ImageTransfer.Seq)
			if env.ImageTransfer.DuplicateOf > 0 {
				body = fmt.Sprintf("Duplicate of #%d", env.ImageTransfer.DuplicateOf)
			} else {
				body = fmt.Sprintf("%s %dx%d %s",
					env.ImageTransfer.Fingerprint,
					env.ImageTransfer.Width,
					env.ImageTransfer.Height,
					env.ImageTransfer.Format,
				)
			}
		}
	case KindToolAttention, KindGenericMessage:
		if env.GenericMessage != nil {
			title = env.GenericMessage.Title
			body = env.GenericMessage.Body
			if env.Kind == KindGenericMessage && !env.GenericMessage.Verified {
				title = "[unverified] " + title
			}
		}
	default:
		title = "cc-clip"
		body = string(env.Kind)
	}
	return title, body
}
