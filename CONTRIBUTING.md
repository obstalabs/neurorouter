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

Before starting a large change, check that it belongs in the public community repo. New product features do not land in this repository by default. If your idea expands the community boundary rather than maintaining the current edition, open an issue first and reference the work order that approves that boundary change.

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
- Security fixes and trust-hardening changes
- Compatibility updates for supported community clients and providers
- Improvements to existing community filters and protection rules
- Documentation, CI, packaging, and release hygiene improvements
- Non-behavioral refactors that make the community edition easier to maintain

## What we don't accept

- Features that add external dependencies
- ML or probabilistic approaches (deterministic only)
- Telemetry, analytics, or phone-home behavior
- Changes that break the trust architecture (see docs/trust-architecture.md)
- New product features or premium/private capabilities without an explicit work order that expands the community boundary
- Key-gated functionality, hidden paid features, or code that belongs in `neurorouter-pro`

## Testing

All new code must have tests. Run the full suite before submitting:

```bash
go test ./... -race
go vet ./...
go build ./cmd/neurorouter
```

## Questions

Open an issue before starting work on large features. This saves everyone time.

## Repo boundary

This repository is `neurorouter-free`, the public AGPL community edition.

- Allowed by default: fixes, security work, compatibility updates, tests, docs, packaging, and maintenance of the existing community feature set
- Not allowed by default: new premium features, new hosted/control-plane behavior, org/team features, key-gated capability, or roadmap work meant for `neurorouter-pro`

When in doubt, assume the change belongs in `neurorouter-pro` until the community boundary is explicitly updated.
