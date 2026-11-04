//go:build windows

package remoteupload

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func privateDirectory(root string) error {
	// Refuse junctions and other reparse points anywhere in the path before
	// applying permissions; a lexical managed path must not redirect elsewhere.
	for p := root; ; p = filepath.Dir(p) {
		ptr, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(ptr)
		if err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("upload directory cannot traverse a reparse point: %s", p)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("upload directory must be a regular directory")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	// Protect the DACL from inheritance and let new files inherit only the
	// authenticated account and SYSTEM, including on a shared remote host.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}
