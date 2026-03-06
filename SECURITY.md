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
- **Token authentication:** All clipboard API calls require a Bearer token with configurable TTL (default 12h)
- **User-Agent validation:** API requests must include a `cc-clip` User-Agent header
- **Token file permissions:** Token files are written with `chmod 600`
- **SSH tunnel:** All data between local and remote travels through the existing SSH connection
- **Shim isolation:** The shim only intercepts specific Claude Code invocation patterns; all other calls pass through to the real binary unchanged
- **No persistent storage of clipboard data:** Images are served on-demand, not cached

## Scope

The following are **in scope** for security reports:
- Token leakage or bypass
- Daemon accessible from non-loopback interfaces
- Command injection via shim templates
- Unauthorized clipboard access

The following are **out of scope:**
- Attacks requiring local root access (the daemon runs as the user)
- Social engineering
- Denial of service on localhost
