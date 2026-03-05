package daemon

// ClipboardType represents what is currently in the clipboard.
type ClipboardType string

const (
	ClipboardImage ClipboardType = "image"
	ClipboardText  ClipboardType = "text"
	ClipboardEmpty ClipboardType = "empty"
)

// ClipboardInfo holds clipboard content metadata.
type ClipboardInfo struct {
	Type   ClipboardType `json:"type"`
	Format string        `json:"format,omitempty"`
}

// ClipboardReader reads clipboard content from the OS.
type ClipboardReader interface {
	Type() (ClipboardInfo, error)
	ImageBytes() ([]byte, error)
}
