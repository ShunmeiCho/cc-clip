//go:build !windows

package tunnelmgr

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Keep the lock file after unlocking: removing it lets another process lock a
// different inode while an existing waiter still holds the original inode.
func lockService(path string) (func(), error) {
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, stateFileMode)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !controlDirOwnedByCurrentUser(info) {
		f.Close()
		if err == nil {
			err = errors.New("service lock must be a user-owned regular file")
		}
		return nil, err
	}
	if err := f.Chmod(stateFileMode); err != nil {
		f.Close()
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrServiceAlreadyRunning
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
