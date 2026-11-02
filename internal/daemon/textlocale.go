package daemon

import "strings"

// utf8TextEnv returns the environment for pbcopy and pbpaste.
//
// Both tools take their text encoding from the locale environment, and "if an
// encoding cannot be determined from the locale, the standard C encoding will
// be used" (pbcopy(1)). Under launchd the daemon gets only what its plist sets,
// which is PATH alone, so UTF-8 text went through pbcopy as Mac Roman and CJK
// text reached the local clipboard as mojibake.
//
// The result is a copy of env without LC_ALL and LC_CTYPE, with
// LC_CTYPE=UTF-8 appended; everything else, LANG included, is kept. LC_ALL is
// dropped because it would override LC_CTYPE. LC_CTYPE is set even when the
// environment already looks UTF-8: guessing from a locale name can be wrong
// (a glibc-style "en_US.utf8" is not a macOS locale), and LC_CTYPE=UTF-8 is
// the setting that was checked to round-trip CJK text through pbcopy on macOS.
func utf8TextEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "LC_ALL=") || strings.HasPrefix(kv, "LC_CTYPE=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LC_CTYPE=UTF-8")
}
