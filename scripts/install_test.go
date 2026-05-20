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
