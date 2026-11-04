package remoteupload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteSecret creates a private directory and atomically persists credentials.
func WriteSecret(root, name string, data []byte) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:`) {
		return "", fmt.Errorf("secret name must be a plain filename")
	}
	if err := privateDirectory(root); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(root, ".secret-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(root, name)
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
