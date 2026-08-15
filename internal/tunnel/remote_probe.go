package tunnel

import (
	"fmt"
	"strings"
)

// Markers the remote probe script prints. They are matched as substrings, so
// they must not be prefixes of one another and must be unlikely to appear in
// unrelated shell noise.
const (
	probeMarkerOK         = "cc-clip-probe:ok"
	probeMarkerDown       = "cc-clip-probe:down"
	probeMarkerStale      = "cc-clip-probe:stale"
	probeMarkerUnverified = "cc-clip-probe:unverified"
)

// RemoteTunnelState is the outcome of probing the forwarded port from the
// remote side.
//
// The distinction that matters is Down vs Stale. A TCP-only check cannot tell
// them apart: an sshd child left over from a previous session keeps the LISTEN
// socket on the forwarded port, so the handshake succeeds while nothing can
// actually reach the daemon. Reporting that as a healthy tunnel is worse than
// reporting nothing, because the operator stops looking.
type RemoteTunnelState string

const (
	// RemoteTunnelOK means a cc-clip daemon answered GET /health through the
	// tunnel. This is the only state that proves the data path works.
	RemoteTunnelOK RemoteTunnelState = "ok"

	// RemoteTunnelDown means nothing is listening on the forwarded port.
	// Normal when no SSH session with RemoteForward is currently open.
	RemoteTunnelDown RemoteTunnelState = "down"

	// RemoteTunnelStale means the port accepted a TCP connection but no
	// cc-clip daemon answered. Either a stale sshd from a previous session
	// still owns the port, or the forward is live and the local daemon is
	// not running.
	RemoteTunnelStale RemoteTunnelState = "stale"

	// RemoteTunnelUnverified means the port is reachable but the remote has
	// no HTTP client to confirm the daemon with. Deliberately not folded
	// into OK: reachability is not liveness.
	RemoteTunnelUnverified RemoteTunnelState = "unverified"

	// RemoteTunnelUnknown means the probe output could not be interpreted,
	// so the check did not actually run to completion.
	RemoteTunnelUnknown RemoteTunnelState = "unknown"
)

// Healthy reports whether the state proves the data path works. Only
// RemoteTunnelOK qualifies; every other state, including Unverified and
// Unknown, fails closed.
func (s RemoteTunnelState) Healthy() bool {
	return s == RemoteTunnelOK
}

// Summary returns a one-line operator-facing description of the state.
func (s RemoteTunnelState) Summary(port int) string {
	switch s {
	case RemoteTunnelOK:
		return fmt.Sprintf("port %d forwarded and cc-clip daemon answering", port)
	case RemoteTunnelDown:
		return fmt.Sprintf("port %d not reachable from remote", port)
	case RemoteTunnelStale:
		return fmt.Sprintf(
			"port %d accepts connections but no cc-clip daemon answered "+
				"(stale sshd forward from a previous session, or the local daemon is not running)",
			port)
	case RemoteTunnelUnverified:
		return fmt.Sprintf("port %d reachable but daemon liveness unverified (no curl on remote)", port)
	default:
		return fmt.Sprintf("port %d check did not complete (unrecognized probe output)", port)
	}
}

// RemoteHealthProbeCommand builds a POSIX sh command to run ON THE REMOTE that
// classifies the forwarded port. It first checks TCP reachability, then asks
// the daemon to identify itself via GET /health, and prints exactly one marker.
//
// /health is the only unauthenticated cc-clip endpoint, so the probe needs no
// token and stays usable even when the remote token is stale.
func RemoteHealthProbeCommand(port int) string {
	return fmt.Sprintf(`if ! bash -c 'echo >/dev/tcp/127.0.0.1/%[1]d' 2>/dev/null; then
  echo '%[2]s'
elif ! command -v curl >/dev/null 2>&1; then
  echo '%[3]s'
else
  _body=$(curl -sf --max-time 5 "http://127.0.0.1:%[1]d/health" 2>/dev/null) || _body=""
  case "$_body" in
    *'"service":"cc-clip"'*) echo '%[4]s' ;;
    *) echo '%[5]s' ;;
  esac
fi`, port, probeMarkerDown, probeMarkerUnverified, probeMarkerOK, probeMarkerStale)
}

// ClassifyRemoteProbeOutput maps the probe script's output to a state. Output
// is matched by substring so trailing shell noise (motd fragments, warnings)
// does not defeat the classification. Anything unrecognized is Unknown rather
// than being optimistically read as success.
func ClassifyRemoteProbeOutput(out string) RemoteTunnelState {
	switch {
	case strings.Contains(out, probeMarkerOK):
		return RemoteTunnelOK
	case strings.Contains(out, probeMarkerStale):
		return RemoteTunnelStale
	case strings.Contains(out, probeMarkerUnverified):
		return RemoteTunnelUnverified
	case strings.Contains(out, probeMarkerDown):
		return RemoteTunnelDown
	default:
		return RemoteTunnelUnknown
	}
}
