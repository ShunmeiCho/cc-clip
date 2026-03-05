package doctor

import (
	"testing"
)

func TestRunLocalReturnsResults(t *testing.T) {
	results := RunLocal(18339)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 checks, got %d", len(results))
	}

	names := make(map[string]bool)
	for _, r := range results {
		names[r.Name] = true
		if r.Name == "" {
			t.Fatal("check result has empty name")
		}
		if r.Message == "" {
			t.Fatalf("check %s has empty message", r.Name)
		}
	}

	for _, expected := range []string{"daemon", "clipboard", "token"} {
		if !names[expected] {
			t.Fatalf("missing check: %s", expected)
		}
	}
}

func TestPrintResults(t *testing.T) {
	results := []CheckResult{
		{"test-pass", true, "all good"},
		{"test-fail", false, "something wrong"},
	}
	allOK := PrintResults(results)
	if allOK {
		t.Fatal("expected allOK=false when one check fails")
	}

	allPass := []CheckResult{
		{"a", true, "ok"},
		{"b", true, "ok"},
	}
	if !PrintResults(allPass) {
		t.Fatal("expected allOK=true when all pass")
	}
}
