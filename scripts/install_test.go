package scripts_test

import (
	"os"
	"regexp"
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

// TestUpgradingDocPipesPinEnvToSh locks the docs/upgrading.md CC_CLIP_VERSION
// pin example into the correct "env-to-sh" form. The pin must reach the `sh`
// that runs the script (right-hand side of the pipe), not `curl`. The broken
// form `CC_CLIP_VERSION=... curl ... | sh` exports the pin only into curl, so
// the piped sh still installs /latest. This is a doc-regression guard, not a
// runtime test.
func TestUpgradingDocPipesPinEnvToSh(t *testing.T) {
	data, err := os.ReadFile("../docs/upgrading.md")
	if err != nil {
		t.Fatalf("read docs/upgrading.md: %v", err)
	}
	doc := string(data)

	// The doc must show the env var on the right-hand side of the pipe, either
	// piping into `CC_CLIP_VERSION=... sh` or exporting it before the pipe.
	hasPipeToShPin := strings.Contains(doc, "| CC_CLIP_VERSION=") &&
		regexp.MustCompile(`\|\s*CC_CLIP_VERSION=\S+\s+sh\b`).MatchString(doc)
	hasExportBeforePipe := regexp.MustCompile(`export\s+CC_CLIP_VERSION=`).MatchString(doc)
	if !hasPipeToShPin && !hasExportBeforePipe {
		t.Fatalf("docs/upgrading.md must pass CC_CLIP_VERSION to the piped sh " +
			"(e.g. `curl ... | CC_CLIP_VERSION=v0.5.0 sh`) or export it before the pipe")
	}

	// Guard against regressing to the broken form where the env var is attached
	// to curl and the script body is piped to a bare `sh` — that pin never
	// reaches the script. Detect `CC_CLIP_VERSION=... curl ...` on one logical
	// command that then pipes to `sh` without re-setting the env on the pipe.
	brokenInline := regexp.MustCompile(`CC_CLIP_VERSION=\S+\s+curl\b[^\n]*\|\s*sh\b`)
	brokenMultiline := regexp.MustCompile(`CC_CLIP_VERSION=\S+\s*\\\s*\n\s*curl\b`)
	for _, code := range extractFencedCode(doc) {
		if brokenInline.MatchString(code) || brokenMultiline.MatchString(code) {
			t.Fatalf("docs/upgrading.md has a broken CC_CLIP_VERSION pin that "+
				"sets the env on curl instead of the piped sh:\n%s", code)
		}
	}
}

// extractFencedCode returns the contents of every ```...``` fenced block in the
// markdown doc, so the regression guard only inspects runnable command examples
// and not the surrounding prose (which deliberately names the broken pattern as
// a counter-example).
func extractFencedCode(doc string) []string {
	var blocks []string
	parts := strings.Split(doc, "```")
	// Fenced blocks are the odd-indexed segments (1, 3, 5, ...).
	for i := 1; i < len(parts); i += 2 {
		blocks = append(blocks, parts[i])
	}
	return blocks
}
