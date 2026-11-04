package remoteupload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSecretIsAtomicAndRejectsEscapingNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bridge", "session")
	for _, name := range []string{"", ".", "..", "../token", `..\token`, "token:stream"} {
		if _, err := WriteSecret(root, name, []byte("secret")); err == nil {
			t.Fatalf("accepted secret filename %q", name)
		}
	}
	path, err := WriteSecret(root, "token", []byte("ephemeral-token"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "ephemeral-token" {
		t.Fatalf("invalid persisted secret: %q %v", data, err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 || files[0].Name() != "token" {
		t.Fatalf("secret staging file left behind: %v %v", files, err)
	}
}
