//go:build windows

package daemon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"syscall"
)

func readClipboardUnicodeText() (string, bool, error) {
	data, ok, err := readClipboardGlobalBytes(cfUnicodeText, int64(maxTextSize())*2+2, fmt.Sprintf("clipboard text exceeds %dMB limit", maxTextMB()))
	if !ok || err != nil {
		return "", ok, err
	}
	text, err := decodeUnicodeClipboardText(data)
	return text, true, err
}

func decodeUnicodeClipboardText(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("clipboard Unicode text has odd byte length")
	}
	codeUnits := len(data) / 2
	if codeUnits > maxTextSize() {
		return "", clipboardOutputLimitError{msg: fmt.Sprintf("clipboard text exceeds %dMB limit", maxTextMB())}
	}
	u16 := make([]uint16, codeUnits)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(data[i*2 : i*2+2])
	}
	text := syscall.UTF16ToString(u16)
	if text == "" {
		return "", fmt.Errorf("clipboard text is empty")
	}
	if len(text) > maxTextSize() {
		return "", clipboardOutputLimitError{msg: fmt.Sprintf("clipboard text exceeds %dMB limit", maxTextMB())}
	}
	return text, nil
}

func readClipboardANSIText() (string, bool, error) {
	data, ok, err := readClipboardGlobalBytes(cfText, int64(maxTextSize())+1, fmt.Sprintf("clipboard text exceeds %dMB limit", maxTextMB()))
	if !ok || err != nil {
		return "", ok, err
	}
	text, err := decodeANSIClipboardText(data)
	return text, true, err
}

func decodeANSIClipboardText(data []byte) (string, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		data = data[:i]
	}
	if len(data) == 0 {
		return "", fmt.Errorf("clipboard text is empty")
	}
	if len(data) > maxTextSize() {
		return "", clipboardOutputLimitError{msg: fmt.Sprintf("clipboard text exceeds %dMB limit", maxTextMB())}
	}
	return string(data), nil
}
