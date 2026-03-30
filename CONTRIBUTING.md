# Contributing to NeuroRouter

## Before you start

All contributions require signing the [Contributor License Agreement](docs/CLA.md). This is enforced automatically on pull requests.

**Why?** NeuroRouter uses an AGPL community edition plus commercial licensing and service offerings from Obsta Labs, LLC. The CLA grants rights that allow contributions to be distributed under both open-source and commercial terms. You retain ownership of your work.

## How to contribute

1. Fork the repository
2. Create a branch: `git checkout -b feat/your-feature`
3. Make your changes
4. Run tests: `go test ./... -race`
5. Run vet: `go vet ./...`
6. Commit with a conventional message: `feat: add feature`
7. Push and open a pull request

## Commit messages

Format: `type: concise imperative statement`

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `perf`

One line, max 72 characters. Say what changed, not every detail of how.

## Code style

- Go standard formatting (`gofmt`)
- No external dependencies (stdlib only)
- Tests alongside source files
- `go test -race` must pass
- Comments explain why, not what

## What we accept

- Bug fixes with tests
- Filter improvements (new patterns, better detection)
- Secret detection rules (new credential types)
- Rotation recipes (new services)
- Documentation improvements

## What we don't accept

- Features that add external dependencies
- ML or probabilistic approaches (deterministic only)
- Telemetry, analytics, or phone-home behavior
- Changes that break the trust architecture (see docs/trust-architecture.md)

## Testing

All new code must have tests. Run the full suite before submitting:

```bash
go test ./... -race
go vet ./...
go build ./cmd/neurorouter
```

## Questions

Open an issue before starting work on large features. This saves everyone time.
