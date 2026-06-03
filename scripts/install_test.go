package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestInstallScriptVerifiesReleaseChecksumBeforeExtraction(t *testing.T) {
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	script := string(data)

	for _, needle := range []string{
		"checksums.txt",
		"verify_checksum()",
		"sha256sum",
		"shasum -a 256",
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("install.sh must contain %q", needle)
		}
	}

	verifyIdx := strings.Index(script, "verify_checksum")
	extractIdx := strings.Index(script, "tar -xzf")
	if verifyIdx == -1 || extractIdx == -1 || verifyIdx > extractIdx {
		t.Fatalf("install.sh must verify the archive checksum before extraction")
	}
}

func TestInstallScriptSupportsCCClipVersionPin(t *testing.T) {
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	script := string(data)

	for _, needle := range []string{
		// Reads the pin env var.
		"CC_CLIP_VERSION",
		// Normalizes a leading 'v' (accepts both 0.9.0 and v0.9.0).
		"resolve_version()",
		// Validates a semver-like tag before use.
		"^v[0-9]+\\.[0-9]+\\.[0-9]+",
		// Tells the user the pinned tag is being installed.
		"pinned via CC_CLIP_VERSION",
		// Rejects garbage instead of silently falling back to latest.
		"is not a valid version tag",
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("install.sh must contain %q for CC_CLIP_VERSION pinning", needle)
		}
	}

	// The pin must still route through the existing asset-naming + checksum
	// contract: VERSION is consumed by ARCHIVE_NAME and the download URLs.
	resolveIdx := strings.Index(script, "resolve_version")
	archiveIdx := strings.Index(script, "ARCHIVE_NAME=")
	if resolveIdx == -1 || archiveIdx == -1 || resolveIdx > archiveIdx {
		t.Fatalf("install.sh must resolve the version before building ARCHIVE_NAME")
	}

	// The default (no env) path must remain /releases/latest.
	if !strings.Contains(script, "releases/latest") {
		t.Fatalf("install.sh must keep the /releases/latest default path")
	}
}
