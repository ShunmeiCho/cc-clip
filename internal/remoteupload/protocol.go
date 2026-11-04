// Package remoteupload implements a bounded, binary-safe SSH upload protocol.
// Authentication is supplied by SSH; no additional network listener is opened.
package remoteupload

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const ProtocolVersion = 1
const MaxImageBytes int64 = 20 << 20
const maxHeaderBytes = 4096

type Header struct {
	Protocol  int    `json:"protocol"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Extension string `json:"extension"`
}

type Result struct {
	Protocol int    `json:"protocol"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

func (h Header) Validate() error {
	if h.Protocol != ProtocolVersion {
		return fmt.Errorf("unsupported upload protocol: %d", h.Protocol)
	}
	if h.Size <= 0 || h.Size > MaxImageBytes {
		return fmt.Errorf("image size must be between 1 and %d bytes", MaxImageBytes)
	}
	digest, err := hex.DecodeString(h.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("invalid SHA256")
	}
	switch h.Extension {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic":
	default:
		return fmt.Errorf("unsupported image extension: %q", h.Extension)
	}
	return nil
}

// Receive consumes one JSON line and exactly Size raw bytes, checks their hash,
// and commits the file only after the entire frame has been verified.
func Receive(in io.Reader, root string) (_ Result, err error) {
	r := bufio.NewReaderSize(in, maxHeaderBytes)
	line, err := r.ReadSlice('\n')
	if err != nil {
		return Result{}, fmt.Errorf("invalid upload header: %w", err)
	}
	var h Header
	if err := json.Unmarshal(line, &h); err != nil {
		return Result{}, fmt.Errorf("invalid upload header: %w", err)
	}
	if err := h.Validate(); err != nil {
		return Result{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	if err := privateDirectory(root); err != nil {
		return Result{}, err
	}
	f, err := os.CreateTemp(root, ".incoming-*")
	if err != nil {
		return Result{}, err
	}
	temp := f.Name()
	defer func() { f.Close(); os.Remove(temp) }()
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(f, hash), r, h.Size); err != nil {
		return Result{}, fmt.Errorf("truncated upload: %w", err)
	}
	if _, err := r.ReadByte(); err != io.EOF {
		return Result{}, fmt.Errorf("upload contains trailing data or failed to finish")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(digest, h.SHA256) {
		return Result{}, fmt.Errorf("upload SHA256 mismatch")
	}
	if err := f.Close(); err != nil {
		return Result{}, err
	}
	final := filepath.Join(root, "clip-"+strings.TrimPrefix(filepath.Base(temp), ".incoming-")+"."+h.Extension)
	if err := os.Rename(temp, final); err != nil {
		return Result{}, err
	}
	return Result{ProtocolVersion, final, h.Size, digest}, nil
}
