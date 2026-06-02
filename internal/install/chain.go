package install

import (
	"context"
	"errors"
	"fmt"

	"github.com/shunmei/cc-clip/internal/exitcode"
)

// FailureClass categorizes why an InstallSource did not complete an install.
// It drives the chain's fall-back-vs-hard-stop policy (see InstallSourceChain).
type FailureClass int

const (
	ClassNone       FailureClass = iota // no failure (install succeeded or was a no-op)
	UserRefused                         // user declined consent at the gate
	PolicyForbidden                     // policy/config forbids this source
	NetworkFailure                      // remote source unreachable / transient network error
	CLIUnsupported                      // required CLI/tool missing or incompatible
	NotFound                            // requested component/version not available at this source
	VerifyFailed                        // artifact failed verification (TRUST failure — no fall-back)
)

// String renders a FailureClass for diagnostics and log lines. It is total
// over the defined values; unknown values fall through to a sentinel form.
func (f FailureClass) String() string {
	switch f {
	case ClassNone:
		return "ClassNone"
	case UserRefused:
		return "UserRefused"
	case PolicyForbidden:
		return "PolicyForbidden"
	case NetworkFailure:
		return "NetworkFailure"
	case CLIUnsupported:
		return "CLIUnsupported"
	case NotFound:
		return "NotFound"
	case VerifyFailed:
		return "VerifyFailed"
	default:
		return fmt.Sprintf("FailureClass(%d)", int(f))
	}
}

// label renders a human-readable phrase for guidance/error strings.
func (f FailureClass) label() string {
	switch f {
	case UserRefused:
		return "user refused"
	case PolicyForbidden:
		return "policy forbidden"
	case NetworkFailure:
		return "network failure"
	case CLIUnsupported:
		return "CLI unsupported"
	case NotFound:
		return "not found"
	case VerifyFailed:
		return "verification failed"
	default:
		return f.String()
	}
}

// Outcome is the terminal disposition of an InstallSourceChain walk (or one source).
type Outcome int

const (
	OutcomeInstalled Outcome = iota // a source installed the component
	OutcomeFellBack                 // this source could not install but the chain may advance
	OutcomeHardStop                 // the chain stopped without installing; no further sources tried
	OutcomeNoOp                     // nothing to do (already present / idempotent success)
)

// String renders an Outcome for diagnostics. Total over defined values.
func (o Outcome) String() string {
	switch o {
	case OutcomeInstalled:
		return "OutcomeInstalled"
	case OutcomeFellBack:
		return "OutcomeFellBack"
	case OutcomeHardStop:
		return "OutcomeHardStop"
	case OutcomeNoOp:
		return "OutcomeNoOp"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

// InstallOutcome is the rich result returned by a source and by the chain walk.
type InstallOutcome struct {
	Outcome    Outcome
	Failure    FailureClass // ClassNone unless Outcome is OutcomeFellBack/OutcomeHardStop
	SourceName string       // Name() of the source that produced this outcome (best-effort)
	Version    string       // version installed/identified, when known
	Guidance   string       // human-actionable next step on hard-stop / exhaustion
	Err        error        // underlying error, when present
}

// InstallRequest describes what the chain is asked to install.
type InstallRequest struct {
	Component   string
	WantVersion string
}

// InstallSource is one rung of the chain. Implementations are Step 5; Step 4
// ships only mocks.
//
// Invariant: a well-behaved source returns IsLocal()==true => RequiresNetwork()
// ==false. The chain does NOT trust this — decideNext checks both predicates
// independently so a misbehaving source cannot defeat the strict fall-back rule.
type InstallSource interface {
	Name() string
	IsLocal() bool         // true if the source needs no network (bundled/config); false for marketplace
	RequiresNetwork() bool // true if Install may perform network I/O (marketplace rung)
	Install(ctx context.Context, req InstallRequest) InstallOutcome
}

// ConsentGate decides whether a (typically network) source may run.
// On denial it returns (false, UserRefused) or (false, PolicyForbidden).
// On allow it returns (true, ClassNone).
type ConsentGate interface {
	Allowed(ctx context.Context, req InstallRequest) (bool, FailureClass)
}

// InstallSourceChain walks ordered sources applying the failure->action policy.
//
// It is the install-time analogue of daemon.DeliveryChain but with a
// fundamentally different failure policy: where DeliveryChain treats every
// non-context error as interchangeable and uniformly falls through to the next
// adapter, InstallSourceChain applies trust-and-consent semantics. A typed
// FailureClass drives per-class action: a refusal or policy denial may fall
// back ONLY to a local, non-network source (never to another remote); a
// VerifyFailed hard-stops immediately with no fall-back; only network/CLI/
// not-found failures fall back unrestricted.
type InstallSourceChain struct {
	sources []InstallSource
}

// Install walks sources in order applying the FailureClass policy.
// Returns the winning OutcomeInstalled, an idempotent OutcomeNoOp as soon as a
// source reports the component already present, or OutcomeHardStop (on a
// non-fallback failure or exhaustion). Never silently retries a remote source
// after a refusal, and never falls back after a VerifyFailed.
func (c *InstallSourceChain) Install(ctx context.Context, req InstallRequest) InstallOutcome {
	if len(c.sources) == 0 {
		return InstallOutcome{
			Outcome:  OutcomeHardStop,
			Failure:  ClassNone,
			Guidance: "no install sources configured",
			Err:      errors.New("install: empty source chain"),
		}
	}

	for i, src := range c.sources {
		if err := ctx.Err(); err != nil { // honor cancellation, but as a HardStop
			return InstallOutcome{
				Outcome:    OutcomeHardStop,
				Err:        err,
				Guidance:   "install canceled",
				SourceName: src.Name(),
			}
		}

		out := src.Install(ctx, req) // gating happens INSIDE consentSource.Install
		out.SourceName = nameOr(out.SourceName, src.Name())

		switch out.Outcome {
		case OutcomeInstalled, OutcomeNoOp:
			// Terminal success: the source either installed the component or
			// reported it already present (idempotent no-op). Either way the
			// install goal is satisfied, so stop the walk — a later source (e.g.
			// bundled/config) must NOT run after marketplace already succeeded.
			return out
		case OutcomeHardStop:
			return out // source already decided to stop (e.g. VerifyFailed, refusal+no-local-next)
		case OutcomeFellBack:
			if _, stop, stopOut := c.decideNext(i, out.Failure); stop {
				return stopOut // policy says do NOT advance
			}
			continue // advance to i+1
		default:
			return InstallOutcome{
				Outcome:    OutcomeHardStop,
				Failure:    ClassNone,
				Err:        fmt.Errorf("install: unexpected outcome %v from source %q", out.Outcome, src.Name()),
				SourceName: src.Name(),
			}
		}
	}

	// Unreachable under the current fall-back policy: the final source always
	// terminates the walk (Installed/NoOp/HardStop return inline, and decideNext
	// hard-stops at the last index for every FailureClass). Retained as a
	// defensive total return so the function stays well-typed if the policy ever
	// changes to permit advancing past the last source.
	return InstallOutcome{
		Outcome:  OutcomeHardStop,
		Failure:  ClassNone,
		Guidance: "install: source chain exhausted without success",
		Err:      errors.New("install: source chain exhausted without success"),
	}
}

// decideNext decides whether, after source i produced a fall-back with the
// given class, the chain may advance to source i+1. It returns:
//
//	advance == true            -> caller continues to i+1
//	advance == false (stop)    -> caller returns stopOut (an OutcomeHardStop)
//
// This is where the divergence from DeliveryChain lives: refusal/policy may
// only advance into a local non-network source, VerifyFailed never advances,
// and network/CLI/not-found advance unrestricted. The walk index is monotonic
// — decideNext only ever permits i+1, so an already-attempted remote is never
// revisited.
func (c *InstallSourceChain) decideNext(i int, class FailureClass) (advance bool, stop bool, stopOut InstallOutcome) {
	switch class {
	case VerifyFailed:
		// Trust failure: never fall back.
		return false, true, InstallOutcome{
			Outcome:    OutcomeHardStop,
			Failure:    VerifyFailed,
			Guidance:   "artifact verification failed; refusing to try another source",
			Err:        errors.New("install: verification failed"),
			SourceName: c.sources[i].Name(),
		}

	case UserRefused, PolicyForbidden:
		// Strict: may advance ONLY to a source that is local AND needs no network.
		next := i + 1
		if next < len(c.sources) {
			n := c.sources[next]
			if n.IsLocal() && !n.RequiresNetwork() {
				return true, false, InstallOutcome{} // allow fall-back to the local source
			}
		}
		// No eligible local successor (or next requires network): hard-stop.
		return false, true, InstallOutcome{
			Outcome:    OutcomeHardStop,
			Failure:    class,
			Guidance:   guidanceForDenial(class),
			Err:        errors.New("install: blocked by " + class.label()),
			SourceName: c.sources[i].Name(),
		}

	case NetworkFailure, CLIUnsupported, NotFound:
		// Unrestricted fall-back: advance to any next source if one exists.
		if i+1 < len(c.sources) {
			return true, false, InstallOutcome{}
		}
		return false, true, InstallOutcome{
			Outcome:    OutcomeHardStop,
			Failure:    class,
			Guidance:   "no remaining install sources after " + class.label(),
			Err:        errors.New("install: source chain exhausted"),
			SourceName: c.sources[i].Name(),
		}

	default: // ClassNone or unknown reaching decideNext is a programming error
		return false, true, InstallOutcome{
			Outcome: OutcomeHardStop,
			Failure: ClassNone,
			Err:     fmt.Errorf("install: unexpected fall-back class %v", class),
		}
	}
}

// guidanceForDenial returns an actionable hint for a consent/policy denial.
func guidanceForDenial(class FailureClass) string {
	switch class {
	case UserRefused:
		return "marketplace install requires consent; re-run with --yes to consent, or provide a bundled/config source"
	case PolicyForbidden:
		return "marketplace install forbidden by policy; install a bundled/config source instead"
	default:
		return "install blocked by " + class.label()
	}
}

// nameOr returns a if non-empty, else b.
func nameOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ExitCodeFor maps a terminal InstallOutcome to a process exit code.
// OutcomeInstalled/OutcomeNoOp -> exitcode.Success; OutcomeHardStop ->
// exitcode.InstallBlocked. OutcomeFellBack should never escape the chain;
// it is treated as a hard-stop defensively.
func ExitCodeFor(out InstallOutcome) int {
	switch out.Outcome {
	case OutcomeInstalled, OutcomeNoOp:
		return exitcode.Success
	default:
		return exitcode.InstallBlocked
	}
}

// chainConfig is internal mutable config assembled by options.
type chainConfig struct {
	gate              ConsentGate
	noPluginMarket    bool // drop all RequiresNetwork() sources
	consentPreGranted bool // --yes: gate is bypassed (pre-granted)
}

// Option configures NewInstallSourceChain.
type Option func(*chainConfig)

// WithConsentGate sets the gate used to front the first network source.
func WithConsentGate(g ConsentGate) Option { return func(c *chainConfig) { c.gate = g } }

// WithNoPluginMarketplace drops every RequiresNetwork()==true source from the chain.
func WithNoPluginMarketplace() Option { return func(c *chainConfig) { c.noPluginMarket = true } }

// WithYes pre-grants consent so the fronting gate never denies/prompts.
func WithYes() Option { return func(c *chainConfig) { c.consentPreGranted = true } }

// yesGate realizes WithYes: it pre-grants USER consent but refuses to override
// policy. It wraps the supplied gate (which may be nil). On allow it passes
// through; on a PolicyForbidden denial it preserves PolicyForbidden so policy
// stays authoritative; on any other denial (e.g. UserRefused) it overrides the
// refusal and allows. A nil inner gate means "no policy to consult" -> allow.
type yesGate struct{ inner ConsentGate }

func (g yesGate) Allowed(ctx context.Context, req InstallRequest) (bool, FailureClass) {
	if g.inner == nil {
		return true, ClassNone
	}
	ok, fc := g.inner.Allowed(ctx, req)
	if ok {
		return true, ClassNone
	}
	if fc == PolicyForbidden {
		return false, PolicyForbidden // --yes cannot override policy
	}
	return true, ClassNone // UserRefused (or other non-policy denial) overridden by --yes
}

// deniedGate denies with UserRefused; the safe-by-default gate so the chain
// never silently runs an ungated remote source.
type deniedGate struct{}

func (deniedGate) Allowed(context.Context, InstallRequest) (bool, FailureClass) {
	return false, UserRefused
}

// consentSource decorates the first network rung with a consent gate. On
// denial it reports a fall-back carrying the denial class; the chain's
// decideNext then enforces the strict local-only fall-back (or hard-stop).
type consentSource struct {
	inner ConsentGate // resolved gate; pre-granted gate when WithYes
	src   InstallSource
}

func (cs consentSource) Name() string          { return cs.src.Name() }
func (cs consentSource) IsLocal() bool         { return cs.src.IsLocal() }
func (cs consentSource) RequiresNetwork() bool { return cs.src.RequiresNetwork() }

func (cs consentSource) Install(ctx context.Context, req InstallRequest) InstallOutcome {
	if ok, class := cs.inner.Allowed(ctx, req); !ok {
		return InstallOutcome{Outcome: OutcomeFellBack, Failure: class, SourceName: cs.src.Name()}
	}
	return cs.src.Install(ctx, req)
}

// NewInstallSourceChain assembles the chain from ordered sources and options.
//   - If WithNoPluginMarketplace: every source whose RequiresNetwork()==true is
//     dropped (the chain becomes local-only; no gate is involved).
//   - Else: the FIRST remaining RequiresNetwork() source is wrapped with a
//     consent decorator. WithYes makes that decorator pre-granted (allow without
//     prompting); otherwise the supplied ConsentGate (or a default
//     deny-with-UserRefused gate if nil) governs it. Only the first network rung
//     is gated; Step 5 should supply at most one network source.
//   - Local (non-network) sources are never gated.
func NewInstallSourceChain(sources []InstallSource, opts ...Option) *InstallSourceChain {
	cfg := chainConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.noPluginMarket {
		filtered := make([]InstallSource, 0, len(sources))
		for _, s := range sources {
			if s.RequiresNetwork() {
				continue
			}
			filtered = append(filtered, s)
		}
		return &InstallSourceChain{sources: filtered}
	}

	var gate ConsentGate
	switch {
	case cfg.consentPreGranted:
		// --yes pre-grants USER consent but must NOT suppress a policy denial:
		// yesGate overrides UserRefused (and other non-policy denials) while
		// still surfacing PolicyForbidden so policy remains authoritative.
		gate = yesGate{inner: cfg.gate}
	case cfg.gate != nil:
		gate = cfg.gate
	default:
		gate = deniedGate{}
	}

	wrapped := make([]InstallSource, len(sources))
	gatedFirst := false
	for i, s := range sources {
		if !gatedFirst && s.RequiresNetwork() {
			wrapped[i] = consentSource{inner: gate, src: s}
			gatedFirst = true
			continue
		}
		wrapped[i] = s
	}
	return &InstallSourceChain{sources: wrapped}
}
