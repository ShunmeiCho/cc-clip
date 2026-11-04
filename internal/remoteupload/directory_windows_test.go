//go:build windows

package remoteupload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsUploadDirectoryAndFilePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	_, input := frame(t, []byte{0, 1, 255})
	result, err := Receive(strings.NewReader(string(input)), root)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, result.Path} {
		sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		sddl := sd.String()
		if !strings.Contains(sddl, user.User.Sid.String()) || !strings.Contains(sddl, ";;;SY)") ||
			strings.Contains(sddl, ";;;BU)") || strings.Contains(sddl, ";;;WD)") || strings.Contains(sddl, ";;;AU)") {
			t.Fatalf("unexpected upload permissions for %s: %s", p, sddl)
		}
	}
}

func TestWindowsUploadRejectsDirectorySymlinks(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := privateDirectory(filepath.Join(link, "uploads")); err == nil {
		t.Fatal("accepted directory through a symlink")
	}
	if _, err := os.Stat(filepath.Join(target, "uploads")); !os.IsNotExist(err) {
		t.Fatal("rejected directory traversal created a directory")
	}
}
