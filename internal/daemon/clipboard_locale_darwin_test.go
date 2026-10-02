//go:build darwin

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinTextClipboardUnderNonUTF8Locale(t *testing.T) {
	dir := t.TempDir()
	text := "remote text 中文\nsecond line 日本語\n"
	data := filepath.Join(dir, "clipboard")
	for _, name := range []string{"pbcopy", "pbpaste"} {
		operation := "cat > \"$TEST_CLIPBOARD_DATA\""
		if name == "pbpaste" {
			operation = "cat \"$TEST_CLIPBOARD_DATA\""
		}
		script := "#!/bin/sh\n[ \"$LC_ALL\" = en_US.UTF-8 ] || exit 9\n" + operation + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	t.Setenv("LANG", "C")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "C")
	t.Setenv("TEST_CLIPBOARD_DATA", data)
	if err := NewClipboardTextWriter().WriteText(text); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(data)
	if err != nil || string(written) != text {
		t.Fatalf("written text = %q, err = %v; want %q", written, err, text)
	}
	clipboard := NewClipboardReader()
	read, err := clipboard.Text()
	if err != nil || read != text {
		t.Fatalf("read text = %q, err = %v; want %q", read, err, text)
	}
}
