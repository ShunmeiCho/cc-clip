package daemon

import (
	"fmt"
	"strings"
)

// TextMode controls which directions of the text clipboard bridge the daemon
// serves. Image paste never depends on it.
//
// Any process that can read the remote session token can use the text
// endpoints. GET /clipboard/text then reads whatever the user last copied on
// the local machine (including passwords from a password manager) without any
// visible signal, and POST /clipboard/text replaces it. Restricting the mode
// limits what a compromised or untrusted remote process can do.
type TextMode struct {
	Read  bool // GET /clipboard/text, and reporting "text" from /clipboard/type
	Write bool // POST /clipboard/text (remote copy → local clipboard, #128)
}

// TextModeBoth is the default and matches the behavior before TextMode existed.
var TextModeBoth = TextMode{Read: true, Write: true}

// ParseTextMode accepts off, read, write or both (case-insensitive).
func ParseTextMode(s string) (TextMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none":
		return TextMode{}, nil
	case "read":
		return TextMode{Read: true}, nil
	case "write":
		return TextMode{Write: true}, nil
	case "both", "":
		return TextModeBoth, nil
	}
	return TextMode{}, fmt.Errorf("invalid text mode %q (use off, read, write or both)", s)
}

func (m TextMode) String() string {
	switch {
	case m.Read && m.Write:
		return "both"
	case m.Read:
		return "read"
	case m.Write:
		return "write"
	}
	return "off"
}
