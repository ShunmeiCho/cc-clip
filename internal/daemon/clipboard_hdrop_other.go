//go:build !windows

package daemon

// DroppedImageFile is a no-op on non-Windows platforms; the CF_HDROP file-list
// clipboard format is Windows-only.
func DroppedImageFile() string {
	return ""
}
