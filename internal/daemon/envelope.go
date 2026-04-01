package daemon

import "time"

// NotifyKind discriminates the payload carried by a NotifyEnvelope.
type NotifyKind string

const (
	KindImageTransfer  NotifyKind = "image_transfer"
	KindToolAttention  NotifyKind = "tool_attention"
	KindGenericMessage NotifyKind = "generic_message"
)

// NotifyEnvelope is the unified notification model. Exactly one payload
// field is non-nil, matching Kind.
type NotifyEnvelope struct {
	Kind      NotifyKind
	Source    string
	Host      string
	Timestamp time.Time

	ImageTransfer  *ImageTransferPayload
	ToolAttention  *ToolAttentionPayload
	GenericMessage *GenericMessagePayload
}

// ImageTransferPayload carries clipboard image transfer metadata.
type ImageTransferPayload struct {
	SessionID   string
	Seq         int
	Fingerprint string
	ImageData   []byte
	Format      string
	Width       int
	Height      int
	DuplicateOf int
}

// ToolAttentionPayload carries hook/tool attention metadata (future use).
type ToolAttentionPayload struct {
	SessionID  string
	HookType   string
	StopReason string
	NotifType  string
	ToolName   string
	ToolInput  string
	Message    string
	Verified   bool
}

// GenericMessagePayload carries a freeform notification (future use).
type GenericMessagePayload struct {
	Title      string
	Body       string
	Urgency    int
	Verified   bool
	Subtitle   string
	DedupCount int
}
