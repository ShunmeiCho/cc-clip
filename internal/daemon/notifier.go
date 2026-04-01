package daemon

import (
	"context"
	"time"
)

// NotifyEvent carries image transfer metadata for notification delivery.
type NotifyEvent struct {
	SessionID   string
	Seq         int
	Fingerprint string
	ImageData   []byte
	Format      string
	Width       int
	Height      int
	DuplicateOf int // 0 = unique, N = matches seq N
}

// Notifier delivers transfer notifications to the user.
type Notifier interface {
	Notify(ctx context.Context, event NotifyEvent) error
}

// NopNotifier is a no-op implementation for platforms without notification support.
type NopNotifier struct{}

func (NopNotifier) Notify(context.Context, NotifyEvent) error { return nil }

// newImageTransferEnvelope converts clipboard transfer metadata into
// a NotifyEnvelope. This bridges the legacy NotifyEvent path into the
// unified envelope model.
func newImageTransferEnvelope(source string, payload ImageTransferPayload) NotifyEnvelope {
	return NotifyEnvelope{
		Kind:      KindImageTransfer,
		Source:    source,
		Timestamp: time.Now().UTC(),
		ImageTransfer: &ImageTransferPayload{
			SessionID:   payload.SessionID,
			Seq:         payload.Seq,
			Fingerprint: payload.Fingerprint,
			ImageData:   payload.ImageData,
			Format:      payload.Format,
			Width:       payload.Width,
			Height:      payload.Height,
			DuplicateOf: payload.DuplicateOf,
		},
	}
}
