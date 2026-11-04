//go:build !windows

package remoteupload

import "os"

func privateDirectory(root string) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return os.ErrInvalid
	}
	return os.Chmod(root, 0700)
}
