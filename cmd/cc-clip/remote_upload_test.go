package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shunmei/cc-clip/internal/remoteupload"
)

func TestRemoteUploadCommandRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte{0, 255, 10, 13, 128}
	digest := sha256.Sum256(data)
	h := remoteupload.Header{Protocol: 1, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Extension: "png"}
	meta, _ := json.Marshal(h)
	var out bytes.Buffer
	if err := runRemote([]string{"upload"}, bytes.NewReader(append(append(meta, '\n'), data...)), &out); err != nil {
		t.Fatal(err)
	}
	var receipt remoteupload.Result
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(receipt.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("upload command corrupted image: %v", err)
	}
	cache, _ := os.UserCacheDir()
	if filepath.Dir(receipt.Path) != filepath.Join(cache, "cc-clip", "uploads") {
		t.Fatalf("wrong upload directory: %s", receipt.Path)
	}
	out.Reset()
	if err := runRemote([]string{"probe"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	var probe uploadProbe
	if err := json.Unmarshal(out.Bytes(), &probe); err != nil || probe.OS != runtime.GOOS || probe.Protocol != 1 {
		t.Fatalf("invalid helper capabilities: %+v %v", probe, err)
	}
}

func TestWindowsUploadDirectoryRejectsSharedPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows directory policy")
	}
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	managed := filepath.Join(cache, "cc-clip", "uploads")
	if got, err := receiverUploadDirectory("session"); err != nil || got != filepath.Join(managed, "session") {
		t.Fatalf("relative managed subdirectory rejected: %q %v", got, err)
	}
	for _, dir := range []string{managed, filepath.Join(managed, "中文 with spaces")} {
		if got, err := receiverUploadDirectory(dir); err != nil || got != dir {
			t.Fatalf("valid managed directory rejected: %q %v", got, err)
		}
	}
	for _, dir := range []string{cache, managed + "-other", filepath.Join(managed, "..", "bin"), "~", `C:\Windows`, `C:relative`, "session:stream"} {
		if _, err := receiverUploadDirectory(dir); err == nil {
			t.Fatalf("accepted unmanaged directory: %q", dir)
		}
	}
}
