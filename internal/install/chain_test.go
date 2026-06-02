package install

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mockSource is a configurable InstallSource used by the chain tests. It
// records how many times Install was invoked so tests can assert that a source
// was (or was not) reached, which proves the never-loop-back / strict
// fall-back guarantees rather than merely the final Outcome.
type mockSource struct {
	name      string
	local     bool
	network   bool
	out       InstallOutcome
	callCount int
}

func (m *mockSource) Name() string          { return m.name }
func (m *mockSource) IsLocal() bool         { return m.local }
func (m *mockSource) RequiresNetwork() bool { return m.network }

func (m *mockSource) Install(ctx context.Context, req InstallRequest) InstallOutcome {
	m.callCount++
	return m.out
}

// mockGate is a configurable ConsentGate. It records prompt invocations so
// tests can assert the gate fronted exactly the first network rung and was
// bypassed under WithYes.
type mockGate struct {
	allow       bool
	denyClass   FailureClass
	promptCount int
}

func (g *mockGate) Allowed(ctx context.Context, req InstallRequest) (bool, FailureClass) {
	g.promptCount++
	if g.allow {
		return true, ClassNone
	}
	return false, g.denyClass
}

func installed(name, version string) InstallOutcome {
	return InstallOutcome{Outcome: OutcomeInstalled, SourceName: name, Version: version}
}

func fellBack(class FailureClass) InstallOutcome {
	return InstallOutcome{Outcome: OutcomeFellBack, Failure: class}
}

func noOp() InstallOutcome { return InstallOutcome{Outcome: OutcomeNoOp} }

func hardStop(class FailureClass) InstallOutcome {
	return InstallOutcome{Outcome: OutcomeHardStop, Failure: class}
}

// TestInstallSourceChain_FailureClassPolicy exercises every FailureClass
// branch through the chain walk, asserting the divergence from DeliveryChain:
// refusal/policy fall back ONLY to a local non-network source; VerifyFailed
// never falls back; network/CLI/NotFound fall back unrestricted.
func TestInstallSourceChain_FailureClassPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// sources is built fresh per case so callCount is meaningful.
		sources       []*mockSource
		wantOutcome   Outcome
		wantFailure   FailureClass
		wantSource    string
		wantVersion   string
		wantCallCount []int // expected callCount per source, len == len(sources)
		guidanceHas   string
	}{
		{
			name: "first success stops walk",
			sources: []*mockSource{
				{name: "remote", network: true, out: installed("remote", "1.2.3")},
				{name: "local", local: true, out: installed("local", "0.0.1")},
			},
			wantOutcome:   OutcomeInstalled,
			wantSource:    "remote",
			wantVersion:   "1.2.3",
			wantCallCount: []int{1, 0},
		},
		{
			name: "verify failed hard stops with no fall back",
			sources: []*mockSource{
				{name: "remote", network: true, out: fellBack(VerifyFailed)},
				{name: "local", local: true, out: installed("local", "9")},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   VerifyFailed,
			wantSource:    "remote",
			wantCallCount: []int{1, 0},
			guidanceHas:   "verification failed",
		},
		{
			name: "user refused falls back to local non-network source",
			sources: []*mockSource{
				{name: "remote", network: true, out: fellBack(UserRefused)},
				{name: "bundled", local: true, out: installed("bundled", "2")},
			},
			wantOutcome:   OutcomeInstalled,
			wantSource:    "bundled",
			wantVersion:   "2",
			wantCallCount: []int{1, 1},
		},
		{
			name: "user refused hard stops when next source requires network",
			sources: []*mockSource{
				{name: "remoteA", network: true, out: fellBack(UserRefused)},
				{name: "remoteB", network: true, out: installed("remoteB", "x")},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   UserRefused,
			wantSource:    "remoteA",
			wantCallCount: []int{1, 0},
			guidanceHas:   "consent",
		},
		{
			name: "user refused hard stops when no successor exists",
			sources: []*mockSource{
				{name: "remote", network: true, out: fellBack(UserRefused)},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   UserRefused,
			wantSource:    "remote",
			wantCallCount: []int{1},
		},
		{
			name: "policy forbidden falls back to local non-network source",
			sources: []*mockSource{
				{name: "remote", network: true, out: fellBack(PolicyForbidden)},
				{name: "config", local: true, out: installed("config", "3")},
			},
			wantOutcome:   OutcomeInstalled,
			wantSource:    "config",
			wantVersion:   "3",
			wantCallCount: []int{1, 1},
		},
		{
			name: "policy forbidden hard stops when next requires network",
			sources: []*mockSource{
				{name: "remoteA", network: true, out: fellBack(PolicyForbidden)},
				{name: "remoteB", network: true, out: installed("remoteB", "z")},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   PolicyForbidden,
			wantSource:    "remoteA",
			wantCallCount: []int{1, 0},
			guidanceHas:   "policy",
		},
		{
			name: "network failure falls back unrestricted to remote successor",
			sources: []*mockSource{
				{name: "remoteA", network: true, out: fellBack(NetworkFailure)},
				{name: "remoteB", network: true, out: installed("remoteB", "4")},
			},
			wantOutcome:   OutcomeInstalled,
			wantSource:    "remoteB",
			wantVersion:   "4",
			wantCallCount: []int{1, 1},
		},
		{
			name: "network failure hard stops when exhausted",
			sources: []*mockSource{
				{name: "remote", network: true, out: fellBack(NetworkFailure)},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   NetworkFailure,
			wantSource:    "remote",
			wantCallCount: []int{1},
			guidanceHas:   "network failure",
		},
		{
			name: "cli unsupported falls back unrestricted",
			sources: []*mockSource{
				{name: "remote", network: true, out: fellBack(CLIUnsupported)},
				{name: "local", local: true, out: installed("local", "5")},
			},
			wantOutcome:   OutcomeInstalled,
			wantSource:    "local",
			wantVersion:   "5",
			wantCallCount: []int{1, 1},
		},
		{
			name: "not found falls back unrestricted",
			sources: []*mockSource{
				{name: "remoteA", network: true, out: fellBack(NotFound)},
				{name: "remoteB", network: true, out: installed("remoteB", "6")},
			},
			wantOutcome:   OutcomeInstalled,
			wantSource:    "remoteB",
			wantVersion:   "6",
			wantCallCount: []int{1, 1},
		},
		{
			name: "all no-op stops at first no-op (idempotent success)",
			sources: []*mockSource{
				{name: "a", local: true, out: noOp()},
				{name: "b", local: true, out: noOp()},
			},
			wantOutcome:   OutcomeNoOp,
			wantSource:    "a",
			wantCallCount: []int{1, 0},
		},
		{
			// The user-requested guarantee: a no-op from the first source stops the
			// walk, and a later source that WOULD have installed is never invoked.
			// Proves no-op is terminal (not merely skipped): outcome is NoOp by "a",
			// not Installed by "b".
			name: "first no-op stops walk and does not call later installing source",
			sources: []*mockSource{
				{name: "a", local: true, out: noOp()},
				{name: "b", local: true, out: installed("b", "9")},
			},
			wantOutcome:   OutcomeNoOp,
			wantSource:    "a",
			wantCallCount: []int{1, 0},
		},
		{
			name: "source returning hard stop directly stops walk",
			sources: []*mockSource{
				{name: "a", local: true, out: hardStop(VerifyFailed)},
				{name: "b", local: true, out: installed("b", "7")},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   VerifyFailed,
			wantSource:    "a",
			wantCallCount: []int{1, 0},
		},
		{
			// A fall-back (NotFound) advances to b, which reports the component
			// already present (no-op). Because no-op is now terminal success, the
			// chain returns OutcomeNoOp attributed to b — it does NOT treat the
			// earlier fall-back as an exhaustion failure.
			name: "fall back then no-op returns idempotent success",
			sources: []*mockSource{
				{name: "a", local: true, out: fellBack(NotFound)},
				{name: "b", local: true, out: noOp()},
			},
			wantOutcome:   OutcomeNoOp,
			wantFailure:   ClassNone,
			wantSource:    "b",
			wantCallCount: []int{1, 1},
		},
		{
			// Last source falls back with a non-restricted class and has no
			// successor: decideNext returns a class-specific hard-stop (NOT the
			// aggregated exhaustion message). This is the spec's literal walk.
			name: "last source fall back hard stops with class guidance",
			sources: []*mockSource{
				{name: "a", local: true, out: fellBack(NotFound)},
				{name: "b", local: true, out: fellBack(CLIUnsupported)},
			},
			wantOutcome:   OutcomeHardStop,
			wantFailure:   CLIUnsupported,
			wantSource:    "b",
			wantCallCount: []int{1, 1},
			guidanceHas:   "no remaining install sources",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srcs := make([]InstallSource, len(tt.sources))
			for i, s := range tt.sources {
				srcs[i] = s
			}
			chain := &InstallSourceChain{sources: srcs}
			got := chain.Install(context.Background(), InstallRequest{Component: "shim"})

			if got.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %v, want %v", got.Outcome, tt.wantOutcome)
			}
			if got.Failure != tt.wantFailure {
				t.Errorf("Failure = %v, want %v", got.Failure, tt.wantFailure)
			}
			if tt.wantSource != "" && got.SourceName != tt.wantSource {
				t.Errorf("SourceName = %q, want %q", got.SourceName, tt.wantSource)
			}
			if tt.wantVersion != "" && got.Version != tt.wantVersion {
				t.Errorf("Version = %q, want %q", got.Version, tt.wantVersion)
			}
			if tt.guidanceHas != "" && !strings.Contains(got.Guidance, tt.guidanceHas) {
				t.Errorf("Guidance = %q, want substring %q", got.Guidance, tt.guidanceHas)
			}
			for i, want := range tt.wantCallCount {
				if got := tt.sources[i].callCount; got != want {
					t.Errorf("source[%d] %q callCount = %d, want %d", i, tt.sources[i].name, got, want)
				}
			}
		})
	}
}

// TestInstallSourceChain_EmptyChain asserts a zero-source chain hard-stops
// with actionable guidance rather than silently succeeding.
func TestInstallSourceChain_EmptyChain(t *testing.T) {
	t.Parallel()
	chain := &InstallSourceChain{}
	got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
	if got.Outcome != OutcomeHardStop {
		t.Fatalf("Outcome = %v, want OutcomeHardStop", got.Outcome)
	}
	if got.Err == nil {
		t.Errorf("expected non-nil Err for empty chain")
	}
	if !strings.Contains(got.Guidance, "no install sources") {
		t.Errorf("Guidance = %q, want substring %q", got.Guidance, "no install sources")
	}
}

// TestInstallSourceChain_ContextCanceled asserts cancellation is honored as a
// HardStop before invoking the next source.
func TestInstallSourceChain_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
	chain := &InstallSourceChain{sources: []InstallSource{src}}
	got := chain.Install(ctx, InstallRequest{Component: "shim"})
	if got.Outcome != OutcomeHardStop {
		t.Fatalf("Outcome = %v, want OutcomeHardStop", got.Outcome)
	}
	if !errors.Is(got.Err, context.Canceled) {
		t.Errorf("Err = %v, want context.Canceled", got.Err)
	}
	if src.callCount != 0 {
		t.Errorf("source called %d times, want 0 (cancellation precedes Install)", src.callCount)
	}
}

// TestInstallSourceChain_NeverLoopsBack proves the walk index is monotonic: an
// already-attempted remote that fell back is never re-invoked after later
// sources are tried.
func TestInstallSourceChain_NeverLoopsBack(t *testing.T) {
	t.Parallel()
	a := &mockSource{name: "remoteA", network: true, out: fellBack(NetworkFailure)}
	b := &mockSource{name: "remoteB", network: true, out: fellBack(NetworkFailure)}
	c := &mockSource{name: "local", local: true, out: installed("local", "1")}
	chain := &InstallSourceChain{sources: []InstallSource{a, b, c}}
	got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
	if got.Outcome != OutcomeInstalled || got.SourceName != "local" {
		t.Fatalf("got %+v, want installed by local", got)
	}
	if a.callCount != 1 || b.callCount != 1 || c.callCount != 1 {
		t.Errorf("callCounts a=%d b=%d c=%d, want 1/1/1 (each visited exactly once)", a.callCount, b.callCount, c.callCount)
	}
}

// TestNewInstallSourceChain_ConsentComposition asserts the constructor's
// option matrix: deny-by-default, explicit gate, WithYes, WithNoPluginMarketplace.
func TestNewInstallSourceChain_ConsentComposition(t *testing.T) {
	t.Parallel()

	t.Run("default deny-by-default gates first network rung", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		local := &mockSource{name: "local", local: true, out: installed("local", "2")}
		chain := NewInstallSourceChain([]InstallSource{remote, local})
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		// Deny-by-default => UserRefused => fall back to local non-network.
		if got.Outcome != OutcomeInstalled || got.SourceName != "local" {
			t.Fatalf("got %+v, want installed by local (remote denied by default)", got)
		}
		if remote.callCount != 0 {
			t.Errorf("remote.callCount = %d, want 0 (gate denied before Install)", remote.callCount)
		}
	})

	t.Run("explicit allow gate runs first network rung", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		gate := &mockGate{allow: true}
		chain := NewInstallSourceChain([]InstallSource{remote}, WithConsentGate(gate))
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeInstalled || got.SourceName != "remote" {
			t.Fatalf("got %+v, want installed by remote", got)
		}
		if gate.promptCount != 1 {
			t.Errorf("gate.promptCount = %d, want 1", gate.promptCount)
		}
		if remote.callCount != 1 {
			t.Errorf("remote.callCount = %d, want 1", remote.callCount)
		}
	})

	t.Run("explicit deny gate with policy class hard stops", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		gate := &mockGate{allow: false, denyClass: PolicyForbidden}
		chain := NewInstallSourceChain([]InstallSource{remote}, WithConsentGate(gate))
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeHardStop || got.Failure != PolicyForbidden {
			t.Fatalf("got %+v, want hard stop / PolicyForbidden", got)
		}
		if remote.callCount != 0 {
			t.Errorf("remote.callCount = %d, want 0", remote.callCount)
		}
	})

	t.Run("WithYes overrides UserRefused but still consults the gate for policy", func(t *testing.T) {
		t.Parallel()
		// Under the P1#2 fix --yes pre-grants USER consent without bypassing the
		// gate: the gate is still consulted (so a PolicyForbidden denial would be
		// honored), and a non-policy UserRefused denial is overridden -> install.
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		gate := &mockGate{allow: false, denyClass: UserRefused}
		chain := NewInstallSourceChain([]InstallSource{remote}, WithConsentGate(gate), WithYes())
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeInstalled || got.SourceName != "remote" {
			t.Fatalf("got %+v, want installed by remote (WithYes overrides UserRefused)", got)
		}
		if gate.promptCount != 1 {
			t.Errorf("gate.promptCount = %d, want 1 (WithYes consults gate so policy still applies)", gate.promptCount)
		}
	})

	t.Run("WithNoPluginMarketplace drops all network sources", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		local := &mockSource{name: "local", local: true, out: installed("local", "2")}
		gate := &mockGate{allow: true}
		chain := NewInstallSourceChain([]InstallSource{remote, local}, WithConsentGate(gate), WithNoPluginMarketplace())
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeInstalled || got.SourceName != "local" {
			t.Fatalf("got %+v, want installed by local (network sources dropped)", got)
		}
		if remote.callCount != 0 {
			t.Errorf("remote.callCount = %d, want 0 (dropped)", remote.callCount)
		}
		if gate.promptCount != 0 {
			t.Errorf("gate.promptCount = %d, want 0 (no network rung to gate)", gate.promptCount)
		}
	})

	t.Run("WithNoPluginMarketplace plus WithYes is local-only", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		local := &mockSource{name: "local", local: true, out: noOp()}
		chain := NewInstallSourceChain([]InstallSource{remote, local}, WithNoPluginMarketplace(), WithYes())
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeNoOp {
			t.Fatalf("got %+v, want OutcomeNoOp", got)
		}
		if remote.callCount != 0 {
			t.Errorf("remote.callCount = %d, want 0 (dropped)", remote.callCount)
		}
	})

	t.Run("only first network rung is gated", func(t *testing.T) {
		t.Parallel()
		// First remote is gated and denied (UserRefused). Per strict policy, the
		// next source requires network, so the chain hard-stops at remoteA
		// without ever invoking remoteB. This proves only the first rung is
		// wrapped and that decideNext enforces strict fall-back even though
		// remoteB is ungated.
		remoteA := &mockSource{name: "remoteA", network: true, out: installed("remoteA", "1")}
		remoteB := &mockSource{name: "remoteB", network: true, out: installed("remoteB", "2")}
		gate := &mockGate{allow: false, denyClass: UserRefused}
		chain := NewInstallSourceChain([]InstallSource{remoteA, remoteB}, WithConsentGate(gate))
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeHardStop || got.Failure != UserRefused {
			t.Fatalf("got %+v, want hard stop / UserRefused", got)
		}
		if remoteA.callCount != 0 {
			t.Errorf("remoteA.callCount = %d, want 0 (gate denied)", remoteA.callCount)
		}
		if remoteB.callCount != 0 {
			t.Errorf("remoteB.callCount = %d, want 0 (strict fall-back blocks remote successor)", remoteB.callCount)
		}
		if gate.promptCount != 1 {
			t.Errorf("gate.promptCount = %d, want 1 (only first rung gated)", gate.promptCount)
		}
	})
}

// TestNewInstallSourceChain_Yes_DoesNotOverridePolicyForbidden asserts the
// P1#2 fix: --yes pre-grants USER consent but must NOT suppress a policy denial.
// A gate returning (false, PolicyForbidden) under WithYes still surfaces
// PolicyForbidden to the chain. With only a remote source, the chain hard-stops
// with Failure==PolicyForbidden. With a local non-network successor, the chain
// falls back to it (policy denials may fall back to a local source) and installs.
func TestNewInstallSourceChain_Yes_DoesNotOverridePolicyForbidden(t *testing.T) {
	t.Parallel()

	t.Run("policy forbidden hard stops even under --yes (remote only)", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		gate := &mockGate{allow: false, denyClass: PolicyForbidden}
		chain := NewInstallSourceChain([]InstallSource{remote}, WithConsentGate(gate), WithYes())
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeHardStop || got.Failure != PolicyForbidden {
			t.Fatalf("got %+v, want hard stop / PolicyForbidden (--yes cannot override policy)", got)
		}
		if remote.callCount != 0 {
			t.Errorf("remote.callCount = %d, want 0 (policy denied before Install)", remote.callCount)
		}
		if gate.promptCount != 1 {
			t.Errorf("gate.promptCount = %d, want 1 (policy gate still consulted under --yes)", gate.promptCount)
		}
	})

	t.Run("policy forbidden under --yes falls back to local non-network source", func(t *testing.T) {
		t.Parallel()
		remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
		local := &mockSource{name: "local", local: true, out: installed("local", "2")}
		gate := &mockGate{allow: false, denyClass: PolicyForbidden}
		chain := NewInstallSourceChain([]InstallSource{remote, local}, WithConsentGate(gate), WithYes())
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeInstalled || got.SourceName != "local" {
			t.Fatalf("got %+v, want installed by local (policy denial falls back to local)", got)
		}
		if remote.callCount != 0 {
			t.Errorf("remote.callCount = %d, want 0 (policy denied)", remote.callCount)
		}
		if local.callCount != 1 {
			t.Errorf("local.callCount = %d, want 1", local.callCount)
		}
	})
}

// TestNewInstallSourceChain_Yes_PreGrantsConsent asserts that --yes overrides a
// USER refusal (UserRefused) — a non-policy denial — so the network source's
// Install is invoked. This is the consent-granting half of the P1#2 contract
// that must keep working alongside the policy-cannot-be-overridden half.
func TestNewInstallSourceChain_Yes_PreGrantsConsent(t *testing.T) {
	t.Parallel()
	remote := &mockSource{name: "remote", network: true, out: installed("remote", "1")}
	gate := &mockGate{allow: false, denyClass: UserRefused}
	chain := NewInstallSourceChain([]InstallSource{remote}, WithConsentGate(gate), WithYes())
	got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
	if got.Outcome != OutcomeInstalled || got.SourceName != "remote" {
		t.Fatalf("got %+v, want installed by remote (--yes overrides UserRefused)", got)
	}
	if remote.callCount != 1 {
		t.Errorf("remote.callCount = %d, want 1 (consent pre-granted)", remote.callCount)
	}
}

// TestInstallSourceChain_DistinctFromDeliveryChain is the explicit contrast
// against daemon.DeliveryChain's uniform fall-through. DeliveryChain would
// advance past ANY non-context error; InstallSourceChain must NOT advance past
// a VerifyFailed nor past a refusal into a remote source. This test never
// imports internal/daemon — the divergence is asserted purely on the install
// package's own behavior.
func TestInstallSourceChain_DistinctFromDeliveryChain(t *testing.T) {
	t.Parallel()

	// Case 1: A DeliveryChain-style uniform fall-through would try the second
	// source after the first "fails". InstallSourceChain must hard-stop on a
	// trust failure instead.
	t.Run("trust failure does not fall through", func(t *testing.T) {
		t.Parallel()
		first := &mockSource{name: "remote", network: true, out: fellBack(VerifyFailed)}
		second := &mockSource{name: "local", local: true, out: installed("local", "1")}
		chain := &InstallSourceChain{sources: []InstallSource{first, second}}
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeHardStop {
			t.Fatalf("got %+v, want OutcomeHardStop (DeliveryChain would have fallen through)", got)
		}
		if second.callCount != 0 {
			t.Errorf("second.callCount = %d, want 0 (no fall-through on trust failure)", second.callCount)
		}
	})

	// Case 2: A refusal must not silently advance to a second remote (the
	// anti-pattern DeliveryChain's uniform fall-through would commit).
	t.Run("refusal does not advance to second remote", func(t *testing.T) {
		t.Parallel()
		first := &mockSource{name: "remoteA", network: true, out: fellBack(UserRefused)}
		second := &mockSource{name: "remoteB", network: true, out: installed("remoteB", "1")}
		chain := &InstallSourceChain{sources: []InstallSource{first, second}}
		got := chain.Install(context.Background(), InstallRequest{Component: "shim"})
		if got.Outcome != OutcomeHardStop || got.Failure != UserRefused {
			t.Fatalf("got %+v, want hard stop / UserRefused", got)
		}
		if second.callCount != 0 {
			t.Errorf("second.callCount = %d, want 0 (refusal never silently retries another remote)", second.callCount)
		}
	})
}

// TestExitCodeFor maps terminal outcomes to process exit codes.
func TestExitCodeFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  InstallOutcome
		want int
	}{
		{"installed -> success", InstallOutcome{Outcome: OutcomeInstalled}, 0},
		{"no-op -> success", InstallOutcome{Outcome: OutcomeNoOp}, 0},
		{"hard stop -> install blocked", InstallOutcome{Outcome: OutcomeHardStop}, 14},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ExitCodeFor(tt.out); got != tt.want {
				t.Errorf("ExitCodeFor(%v) = %d, want %d", tt.out.Outcome, got, tt.want)
			}
		})
	}
}
