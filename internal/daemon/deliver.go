package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
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
//
// Context cancellation short-circuits the chain: if ctx is already done
// before trying an adapter, or an adapter returns context.Canceled /
// context.DeadlineExceeded, Deliver returns that error immediately
// instead of falling through to the next adapter. Cancellation expresses
// "caller has given up" — falling through to another adapter would
// silently override that intent.
func (c *DeliveryChain) Deliver(ctx context.Context, env NotifyEnvelope) error {
	var lastErr error
	for _, adapter := range c.adapters {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := adapter.Deliver(ctx, env)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		lastErr = err
		log.Printf("delivery adapter %s failed: %v", adapter.Name(), err)
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
	env := newImageTransferEnvelope("clipboard", ImageTransferPayload(evt))
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
	warnIfNoAdapters(adapters)
	return &DeliveryChain{adapters: adapters}
}

// warnIfNoAdapters emits a one-shot startup warning when the delivery chain
// has no usable adapters. Without this, every Deliver call silently returns
// "no delivery adapters configured" with no indication at startup that
// notifications can never be delivered.
func warnIfNoAdapters(adapters []Deliverer) {
	if len(adapters) == 0 {
		log.Printf("WARN: no notification delivery adapters available; notifications will not be delivered")
	}
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

const defaultCriticalSound = "Glass"

// notificationSound resolves the macOS sound name for an envelope.
//
// Precedence:
//  1. An explicit per-notification Sound (e.g. from a generic /notify payload)
//     always wins. An unrecognized explicit name normalizes to silence, so a
//     caller can opt out with "none" / "off" / "silent".
//  2. Otherwise a configurable default-sound policy applies, keyed by urgency
//     tier (see defaultSoundForUrgency).
func notificationSound(env NotifyEnvelope) string {
	if env.GenericMessage == nil {
		return ""
	}
	if strings.TrimSpace(env.GenericMessage.Sound) != "" {
		return normalizeNotificationSound(env.GenericMessage.Sound)
	}
	return defaultSoundForUrgency(env.GenericMessage.Urgency)
}

// defaultSoundForUrgency picks the default sound for a notification that did
// not carry an explicit sound. Each urgency tier reads a local environment
// override so users can configure sounds without redeploying the remote
// agents, then falls back to a built-in default. Built-in defaults preserve
// historical behaviour: only the critical tier (permission prompts) makes a
// sound; attention/calm stay silent unless configured.
//
// An override of none/off/silent (or any unrecognized name) suppresses the
// tier's sound — the user asked for something specific, so we never
// second-guess it with a different built-in sound.
//
// The environment is read on the local daemon (where Deliver runs), not from
// the remote /notify payload, so a remote agent cannot change local sound
// settings.
func defaultSoundForUrgency(urgency int) string {
	var envKey, builtin string
	switch {
	case urgency >= 2:
		envKey, builtin = "CC_CLIP_SOUND_CRITICAL", defaultCriticalSound
	case urgency == 1:
		envKey, builtin = "CC_CLIP_SOUND_ATTENTION", ""
	default:
		envKey, builtin = "CC_CLIP_SOUND_CALM", ""
	}
	if override := strings.TrimSpace(os.Getenv(envKey)); override != "" {
		return normalizeNotificationSound(override)
	}
	return builtin
}

func normalizeNotificationSound(sound string) string {
	switch strings.ToLower(strings.TrimSpace(sound)) {
	case "", "none", "off", "silent":
		return ""
	case "basso":
		return "Basso"
	case "blow":
		return "Blow"
	case "bottle":
		return "Bottle"
	case "frog":
		return "Frog"
	case "funk":
		return "Funk"
	case "glass":
		return "Glass"
	case "hero":
		return "Hero"
	case "morse":
		return "Morse"
	case "ping":
		return "Ping"
	case "pop":
		return "Pop"
	case "purr":
		return "Purr"
	case "sosumi":
		return "Sosumi"
	case "submarine":
		return "Submarine"
	case "tink":
		return "Tink"
	default:
		return ""
	}
}
