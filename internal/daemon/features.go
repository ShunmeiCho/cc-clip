package daemon

import (
	"fmt"
	"net/http"
	"strings"
)

// Features is the allowlist of bridges the daemon serves (serve --features).
// Each one exposes something different to any remote process that can read
// the session token (or, for notifications, the nonce):
//
//   - Image: GET /clipboard/image reads the local clipboard image.
//   - TextRead: GET /clipboard/text reads whatever the user last copied,
//     including passwords from a password manager, with no visible signal.
//   - TextWrite: POST /clipboard/text replaces the local clipboard text (#128).
//   - Notify: POST /notify raises a local notification with remote-controlled
//     text, and image transfers are announced.
//
// A disabled feature answers 403 and never touches the local clipboard or the
// notification queue. Because it is an allowlist, a feature added later stays
// off for anyone who has chosen an explicit list.
type Features struct {
	Image     bool
	TextRead  bool
	TextWrite bool
	Notify    bool
}

// AllFeatures is the default and matches the behavior before Features existed.
var AllFeatures = Features{Image: true, TextRead: true, TextWrite: true, Notify: true}

// ParseFeatures accepts a comma-separated list of image, text (both
// directions), text-read, text-write and notify, or all / none. Empty means
// all. Matching is case-insensitive.
func ParseFeatures(s string) (Features, error) {
	if strings.TrimSpace(s) == "" {
		return AllFeatures, nil
	}
	var f Features
	for _, tok := range strings.Split(s, ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "all":
			f = AllFeatures
		case "none", "":
		case "image":
			f.Image = true
		case "text":
			f.TextRead, f.TextWrite = true, true
		case "text-read":
			f.TextRead = true
		case "text-write":
			f.TextWrite = true
		case "notify":
			f.Notify = true
		default:
			return Features{}, fmt.Errorf("invalid feature %q (use image, text, text-read, text-write, notify, all or none)", strings.TrimSpace(tok))
		}
	}
	return f, nil
}

// String is the canonical form ParseFeatures reads back.
func (f Features) String() string {
	if f == AllFeatures {
		return "all"
	}
	var names []string
	if f.Image {
		names = append(names, "image")
	}
	switch {
	case f.TextRead && f.TextWrite:
		names = append(names, "text")
	case f.TextRead:
		names = append(names, "text-read")
	case f.TextWrite:
		names = append(names, "text-write")
	}
	if f.Notify {
		names = append(names, "notify")
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

// DisabledFeatureHeader names the feature behind a 403 caused by the daemon's
// own --features. Clients use it to tell "turned off on purpose" apart from an
// auth rejection, which can also be a 403 (unknown User-Agent).
const DisabledFeatureHeader = "X-CC-Clip-Disabled"

func (s *Server) featureDisabled(w http.ResponseWriter, feature string) {
	w.Header().Set(DisabledFeatureHeader, feature)
	http.Error(w, feature+" disabled (serve --features="+s.features.String()+")", http.StatusForbidden)
}
