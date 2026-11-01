//go:build darwin

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cjkSample is the kind of text that came out as Mac Roman mojibake before
// pbcopy and pbpaste were given a UTF-8 locale.
const cjkSample = "中文·テスト"

// fakeClipboardTools puts fake pbcopy, pbpaste and pngpaste first on PATH.
// Each fake records the environment it was started with into dir/<name>.env;
// the scripts find dir from $0, so no path is quoted into them. The fake
// pngpaste fails, so Type() falls through to pbpaste even on a machine whose
// real pngpaste would find an image.
func fakeClipboardTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scripts := map[string]string{
		"pbcopy":   "d=$(dirname \"$0\")\n/usr/bin/env > \"$d/pbcopy.env\"\ncat > \"$d/pbcopy.stdin\"\n",
		"pbpaste":  "d=$(dirname \"$0\")\n/usr/bin/env > \"$d/pbpaste.env\"\nprintf '%s' '" + cjkSample + "'\n",
		"pngpaste": "exit 1\n",
	}
	for name, body := range scripts {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// launchdLikeLocale gives the daemon the environment launchd gave it: no LANG
// and no LC_CTYPE. LC_ALL=C is added because it would override any LC_CTYPE
// the daemon sets, so the fix has to remove it.
func launchdLikeLocale(t *testing.T) {
	t.Helper()
	t.Setenv("LANG", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LC_ALL", "C")
	os.Unsetenv("LANG")
	os.Unsetenv("LC_CTYPE")
}

func assertUTF8ChildEnv(t *testing.T, envFile string) {
	t.Helper()
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("fake tool did not record its environment: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	hasCtype := false
	for _, line := range lines {
		if line == "LC_CTYPE=UTF-8" {
			hasCtype = true
		}
		if strings.HasPrefix(line, "LC_ALL=") {
			t.Fatalf("child still sees %q, which overrides LC_CTYPE", line)
		}
	}
	if !hasCtype {
		t.Fatalf("child environment lacks LC_CTYPE=UTF-8:\n%s", data)
	}
}

func TestDarwinWriteTextGivesPbcopyUTF8Locale(t *testing.T) {
	dir := fakeClipboardTools(t)
	launchdLikeLocale(t)

	text := cjkSample + "\nsecond line\n"
	if err := NewClipboardTextWriter().WriteText(text); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	assertUTF8ChildEnv(t, filepath.Join(dir, "pbcopy.env"))
	got, err := os.ReadFile(filepath.Join(dir, "pbcopy.stdin"))
	if err != nil {
		t.Fatalf("read fake pbcopy stdin: %v", err)
	}
	if string(got) != text {
		t.Fatalf("pbcopy got %q, want %q verbatim", got, text)
	}
}

func TestDarwinTextReadGivesPbpasteUTF8Locale(t *testing.T) {
	dir := fakeClipboardTools(t)
	launchdLikeLocale(t)
	c := NewClipboardReader()

	info, err := c.Type()
	if err != nil {
		t.Fatalf("Type: %v", err)
	}
	if info.Type != ClipboardText {
		t.Fatalf("Type = %v, want text", info.Type)
	}
	assertUTF8ChildEnv(t, filepath.Join(dir, "pbpaste.env"))

	if err := os.Remove(filepath.Join(dir, "pbpaste.env")); err != nil {
		t.Fatalf("reset env record: %v", err)
	}
	text, err := c.Text()
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if text != cjkSample {
		t.Fatalf("Text = %q, want %q", text, cjkSample)
	}
	assertUTF8ChildEnv(t, filepath.Join(dir, "pbpaste.env"))
}
