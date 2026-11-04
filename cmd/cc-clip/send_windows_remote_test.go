package main

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/shunmei/cc-clip/internal/remoteupload"
)

func TestWindowsCommandEncodingPreservesUnicodeAndQuotes(t *testing.T) {
	script := `& ` + psLiteral(`C:\Users\O'Brien 中文\cc-clip.exe`) + ` remote probe`
	cmd := windowsEncodedCommand(script)
	data, err := base64.StdEncoding.DecodeString(cmd[strings.LastIndex(cmd, " ")+1:])
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
	}
	got := string(utf16.Decode(units))
	if !strings.HasSuffix(got, script) || !strings.Contains(got, "O''Brien 中文") {
		t.Fatalf("corrupt PowerShell script: %q", got)
	}
	args := rawUploadSSHArgs("-host", cmd)
	if !reflect.DeepEqual(args[len(args)-3:], []string{"--", "-host", cmd}) {
		t.Fatalf("host escaped option terminator: %v", args)
	}
}

func TestWindowsProbeRequiresCompleteCapabilities(t *testing.T) {
	p := uploadProbe{1, "windows", "amd64", `C:\Users\中文`, `C:\Users\中文\AppData\Local`}
	encode := func(p uploadProbe) string {
		data, _ := json.Marshal(p)
		return "banner\n" + windowsProbeBegin + string(data) + windowsProbeEnd + "\nbanner"
	}
	if got, err := parseWindowsUploadProbe(encode(p)); err != nil || got != p {
		t.Fatalf("valid probe failed: %+v %v", got, err)
	}
	for _, change := range []func(*uploadProbe){
		func(p *uploadProbe) { p.Home = "relative" }, func(p *uploadProbe) { p.Cache = "C:\\bad\npath" },
		func(p *uploadProbe) { p.Arch = "x86" }, func(p *uploadProbe) { p.OS = "linux" },
		func(p *uploadProbe) { p.Protocol++ },
	} {
		invalid := p
		change(&invalid)
		if _, err := parseWindowsUploadProbe(encode(invalid)); err == nil {
			t.Fatalf("accepted invalid probe: %+v", invalid)
		}
	}
	if _, err := parseWindowsUploadProbe("{}"); err == nil {
		t.Fatal("accepted probe without markers")
	}
}

func TestWindowsUploadReceiptRequiresVerifiedImage(t *testing.T) {
	h := remoteupload.Header{Protocol: 1, Size: 4, SHA256: strings.Repeat("a", 64), Extension: "png"}
	valid := remoteupload.Result{Protocol: 1, Path: `C:\Users\u\clip.png`, Size: 4, SHA256: h.SHA256}
	check := func(r remoteupload.Result) error {
		data, _ := json.Marshal(r)
		_, err := parseWindowsUploadResult(data, h)
		return err
	}
	if err := check(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*remoteupload.Result){
		func(r *remoteupload.Result) { r.Size++ }, func(r *remoteupload.Result) { r.SHA256 = strings.Repeat("b", 64) },
		func(r *remoteupload.Result) { r.Path = "relative.png" }, func(r *remoteupload.Result) { r.Protocol++ },
	} {
		invalid := valid
		change(&invalid)
		if check(invalid) == nil {
			t.Fatalf("accepted invalid receipt: %+v", invalid)
		}
	}
}

func TestRemotePastePathQuotesWindowsSpaces(t *testing.T) {
	for input, want := range map[string]string{
		`C:\Users\First Last\image.png`: `"C:\Users\First Last\image.png"`,
		`C:\Users\中文\image.png`:         `C:\Users\中文\image.png`,
		`/home/u/image.png`:             `/home/u/image.png`,
	} {
		if got := formatRemotePastePath(input); got != want {
			t.Fatalf("paste path %q: got %q want %q", input, got, want)
		}
	}
}

func TestHomeProbeDistinguishesWSLAndGitBash(t *testing.T) {
	for name, want := range map[string]bool{
		"Linux": false, "Darwin": false, "": false,
		"MSYS_NT-10.0-22631": true, "MINGW64_NT-10.0": true, "CYGWIN_NT-10.0": true,
	} {
		out := "banner\n" + remoteHomeMarkerStart + "/home/u" + remoteHomeMarkerEnd + name + "\n"
		if got := nativeWindowsHomeProbe(out); got != want {
			t.Fatalf("native Windows detection for %q: got %t", name, got)
		}
	}
}
