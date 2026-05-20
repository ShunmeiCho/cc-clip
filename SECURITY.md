# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

## Reporting a Vulnerability

If you discover a security vulnerability in cc-clip, please report it responsibly:

**Email:** shunmei.cho@gmail.com

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

**Do NOT open a public issue for security vulnerabilities.**

I will acknowledge receipt within 48 hours and aim to provide a fix within 7 days for critical issues.

## Security Design

cc-clip is designed with the following security principles:

- **Loopback only:** The daemon listens exclusively on `127.0.0.1`, never on external interfaces
- **Token authentication:** All clipboard API calls require a Bearer token with configurable TTL (default 30d, sliding renewal)
- **CSPRNG tokens:** Clipboard tokens are 32 random bytes generated with `crypto/rand` and hex-encoded
- **Constant-time validation:** Token comparison uses constant-time comparison to avoid token timing leaks
- **Token file permissions:** Token files are written with `chmod 600`
- **SSH tunnel:** All data between local and remote travels through the existing SSH connection
- **Shim isolation:** The shim only intercepts specific `xclip` / `wl-paste` image invocation patterns; all other calls pass through to the real binary unchanged
- **No persistent storage of clipboard data:** Images are served on-demand, not cached

## Threat Model

### What cc-clip is intended to defend against

- Network attackers reaching the local clipboard daemon directly. The daemon binds to loopback and rejects non-loopback listeners.
- Accidental unauthenticated access through the SSH tunnel. Clipboard endpoints require a Bearer token, and notification endpoints use a separate nonce.
- Token leakage through process arguments. Tokens and nonces are sent through stdin or files, not command-line args.
- Transparent shim breakage for unrelated clipboard calls. Non-image `xclip` / `wl-paste` calls are passed through to the real binary.

### What cc-clip is not intended to defend against

- **Other users on the same remote host.** SSH `RemoteForward` exposes the daemon to `127.0.0.1` on the remote host, and loopback is reachable by other local users on that machine. The token is the access control boundary.
- **Compromise of your Unix account.** If another process can read `~/.cache/cc-clip/session.token`, `~/.cache/cc-clip/notify.nonce`, your shell rc files, or your process memory as your user, it can likely use cc-clip as you.
- **A compromised local machine.** cc-clip reads the local clipboard by design; malware on the local machine can already read that data.
- **Denial of service by local users.** cc-clip sets HTTP timeouts and resource guards, but it does not try to prevent all localhost abuse by users who can reach the tunnel.

If you use an untrusted multi-user jump host, do not treat cc-clip's remote loopback port as private. Prefer a trusted single-user development host, a VM/container with only your account, or a workflow that does not expose clipboard access through that host.

## Scope

The following are **in scope** for security reports:
- Token leakage or bypass
- Daemon accessible from non-loopback interfaces
- Command injection via shim templates
- Unauthorized clipboard access

The following are **out of scope:**
- Attacks requiring local root access (the daemon runs as the user)
- Attacks by a process that already has the same Unix user privileges as cc-clip
- Social engineering
- Denial of service on localhost
