# Contributing to cc-clip

Thanks for your interest in contributing to cc-clip!

## Getting Started

```bash
git clone https://github.com/ShunmeiCho/cc-clip.git
cd cc-clip
make build
make test
```

## Development

**Requirements:** Go 1.21+, `pngpaste` (macOS, `brew install pngpaste`)

**Project structure:**
- `cmd/cc-clip/` — CLI entry point
- `internal/daemon/` — Local clipboard HTTP server
- `internal/tunnel/` — Client for fetching through SSH tunnel
- `internal/shim/` — Bash shim templates and remote deployment
- `internal/token/` — Session token management
- `internal/exitcode/` — Segmented exit codes
- `internal/doctor/` — Diagnostic checks

**Commands:**
```bash
make build          # Build binary
make test           # Run all tests
make vet            # Run go vet
make release-local  # Cross-compile for all platforms
```

## How to Contribute

### Bug Reports

Open an [issue](https://github.com/ShunmeiCho/cc-clip/issues) with:
- cc-clip version (`cc-clip version`)
- Local OS and remote OS/arch
- Steps to reproduce
- Output of `cc-clip doctor --host <your-host>` (redact sensitive info)
- Shim debug logs: `CC_CLIP_DEBUG=1 xclip -selection clipboard -t TARGETS -o`

### Feature Requests

Open an issue describing the use case and proposed solution.

### Pull Requests

1. Fork the repo and create a feature branch
2. Write tests for new functionality
3. Ensure `make test` and `make vet` pass
4. Use conventional commit messages: `feat:`, `fix:`, `refactor:`, `docs:`, `test:`
5. Keep PRs focused — one feature or fix per PR

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep files under 400 lines where possible
- Prefer immutability — create new objects rather than mutating
- Add comments for non-obvious logic

## Coordinated Changes

Some changes require updates across multiple files. See `CLAUDE.md` for the full list:
- New API endpoint → `daemon/server.go` + `tunnel/fetch.go` + `shim/template.go`
- New exit code → `exitcode/exitcode.go` + `cmd/cc-clip/main.go` + shim templates
- Token format change → `token/token.go` + shim templates

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
