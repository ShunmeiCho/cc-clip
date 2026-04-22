package main

import (
	"io"
	"strings"
	"testing"
)

func TestParseSendArgsTreatsTrailingImagePathAsFileWithDefaultHost(t *testing.T) {
	opts, err := parseSendArgs([]string{"--paste", `C:\test.png`}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "" {
		t.Fatalf("expected host to remain default-resolved, got %q", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
	if !opts.paste {
		t.Fatal("expected --paste to be true")
	}
}

func TestParseSendArgsTreatsTrailingNonImageAsHost(t *testing.T) {
	opts, err := parseSendArgs([]string{"--paste", "myserver"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != "" {
		t.Fatalf("expected no localFile, got %q", opts.localFile)
	}
}

func TestParseSendArgsAllowsHostThenTrailingFile(t *testing.T) {
	opts, err := parseSendArgs([]string{"myserver", "--paste", `C:\test.png`}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
}

func TestParseSendArgsAllowsPostFlagHostThenFile(t *testing.T) {
	opts, err := parseSendArgs([]string{"--paste", "myserver", `C:\test.png`}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
}

func TestParseSendArgsAllowsFileThenFlagsWithDefaultHost(t *testing.T) {
	opts, err := parseSendArgs([]string{`C:\test.png`, "--paste"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "" {
		t.Fatalf("expected host to remain default-resolved, got %q", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
	if !opts.paste {
		t.Fatal("expected --paste to be true")
	}
}

func TestParseSendArgsAllowsHostAndFileThenFlags(t *testing.T) {
	opts, err := parseSendArgs([]string{"myserver", `C:\test.png`, "--paste"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSendArgs returned error: %v", err)
	}
	if opts.host != "myserver" {
		t.Fatalf("expected host %q, got %q", "myserver", opts.host)
	}
	if opts.localFile != `C:\test.png` {
		t.Fatalf("expected localFile %q, got %q", `C:\test.png`, opts.localFile)
	}
	if !opts.paste {
		t.Fatal("expected --paste to be true")
	}
}

func TestParseSendArgsRejectsFileFlagAndPositionalFile(t *testing.T) {
	_, err := parseSendArgs([]string{"--file", "a.png", "b.png"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot use both --file and positional image path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSendArgsRejectsExtraPositionalsWithHost(t *testing.T) {
	_, err := parseSendArgs([]string{"myserver", "--paste", "a.png", "b.png"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSendArgsRejectsFlagAfterPostFlagPositionals(t *testing.T) {
	_, err := parseSendArgs([]string{"--remote-dir", "uploads", `C:\test.png`, "--paste"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must appear before positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSendArgsRejectsNegativeDelay(t *testing.T) {
	_, err := parseSendArgs([]string{"--delay-ms", "-1"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --delay-ms") {
		t.Fatalf("unexpected error: %v", err)
	}
}
