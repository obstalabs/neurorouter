# NeuroRouter

[![CI](https://github.com/ppiankov/neurorouter/actions/workflows/ci.yml/badge.svg)](https://github.com/ppiankov/neurorouter/actions/workflows/ci.yml)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)

Cleans AI requests before they hit the model. Removes wasted context, blocks secrets, and preserves native client semantics locally.

## Install

```bash
brew install ppiankov/tap/neurorouter
```

Or download from [releases](https://github.com/ppiankov/neurorouter/releases/latest).

## Quick start

```bash
# Start the proxy
neurorouter

# Point your AI tool at it
ANTHROPIC_BASE_URL=http://localhost:4000 claude

# Fallback for Codex / OpenAI tools that only support base URL override
OPENAI_BASE_URL=http://localhost:4000 codex

# See what would be filtered without sending
neurorouter --dry-run
```

By default NeuroRouter listens on `127.0.0.1:4000`. If you really need remote clients, opt in explicitly with `neurorouter --public --listen 0.0.0.0:4000`. On public binds, `/v1/audit` and `/v1/suggestions` stay disabled unless you also pass `--expose-management`.

For Codex, the recommended setup is a provider profile that makes the wire mode explicit:

```toml
[providers.neurorouter]
name = "NeuroRouter"
base_url = "http://127.0.0.1:4000"
wire_api = "responses"
```

That keeps Codex on the native Responses API path instead of relying on a generic base-URL override.

## What NeuroRouter is

A drop-in local proxy that sits between your AI coding tool and the API. Every request passes through three stages:

**Block secrets** — credentials, tokens, and connection strings are intercepted inline. Detected leaks are flagged with rotation recipes and prevention hooks.

**Strip waste** — six filters remove structural noise: stale file reads, thinking blocks, orphaned tool results, failed retries, duplicate system reminders, and oversized content blocks.

Universal filters such as `oversized_blocks` and `stale_reads` apply across providers. Provider-specific cleanup such as thinking removal, duplicate system handling, and orphaned tool-result repair is selected by adapter in the live proxy path.

**Preserve semantics** — Codex/OpenAI clients can stay on the native Responses wire path when the selected upstream supports it. For simpler text-only requests against Chat Completions targets, NeuroRouter can still fall back to compatibility translation.

Works with Claude Code, Codex, Aider, Cursor, Continue.dev, and any tool that supports API base URL override.

## Licensing Model

This repository is the self-hosted community edition of NeuroRouter.

- Source code and community binaries from this repo are available under the GNU Affero General Public License v3.0
- Obsta Labs, LLC also offers commercial licenses for organizations that need non-copyleft terms
- Commercial agreements may also include support, managed deployment, or hosted offerings described at [neurorouter.dev](https://neurorouter.dev)

If code is published in this repository under the AGPL, recipients of that code receive AGPL rights to that code.

## Community Vs Premium

Included in this free community edition:
- local proxy core
- request filtering and secret protection
- audit log and dry-run inspection
- DND suppression controls
- provider adapters and compatibility routing
- native Responses passthrough for compatible upstreams
- local config and CLI workflow

Paid or private-only features do not live in this repository. Those include:
- premium task-routing and cascade logic
- runaway detection and pre-cooldown guidance
- context rescue and checkpoint tooling
- session-awareness and premium spend/risk signals
- org policy tooling, managed deployments, and hosted control-plane work

## What NeuroRouter is NOT

- Not an observability platform — it transforms traffic, not just logs it
- Not a model gateway — it makes requests better, not just routes them
- Not developer surveillance — patterns, not people
- Not ML-based — deterministic filters, no classifier guesswork
- Not a hosted service by default — the community edition runs locally on your machine

## Philosophy

*Principiis obsta* — resist the beginnings.

Remove waste before it's billed. Block secrets before they leave. Preserve client semantics and local trust boundaries before the model sees the request. Deterministic rules, not ML predictions. Local execution, not cloud dependencies. Your API key passes through untouched — we never parse it.

## Trust

- **Loopback by default** — the proxy listens on `127.0.0.1:4000` unless you explicitly opt into a public bind
- **Local-first** — your API key never leaves your machine except to the provider
- **No key inspection** — Authorization header forwarded as-is, never parsed, never stored
- **No phone-home** — zero telemetry, zero outbound calls except to your configured upstream
- **Deterministic** — every transformation is visible, explainable, reproducible
- **Verifiable** — `--dry-run` shows exactly what would be removed before sending

## Commands

```
neurorouter              # start proxy (default)
neurorouter proxy        # explicit proxy start
neurorouter stats        # session statistics
neurorouter explain      # explain detected patterns
neurorouter audit        # transformation log
neurorouter dnd          # toggle do-not-disturb
neurorouter config       # manage configuration
neurorouter version      # print version
```

## Repository Layout

- `cmd/neurorouter` contains the CLI entrypoint and command wiring
- `internal/neurorouter` contains the runtime proxy, routing, filtering, config, and support code

The repo is structured as an app-first Go project. Runtime implementation lives behind `internal/`, while the binary entrypoint stays thin and easy to scan.

## Configuration

```bash
# Config file
neurorouter config init          # create ~/.neurorouter/config.toml with defaults
neurorouter config set key val   # set a value
neurorouter config list          # show all settings
```

Precedence: CLI flag > `NEUROROUTER_*` env var > config file > default.

Current proxy startup keys wired through this precedence path:
- `listen_port`
- `upstream`
- `protect_policy`

For current commercial offerings and pricing, see [neurorouter.dev](https://neurorouter.dev).

## License

[GNU Affero General Public License v3.0](LICENSE) for the community edition in this repository.

See [NOTICE](NOTICE) for copyright and trademark notices. Obsta Labs, LLC also offers commercial licenses for organizations that need terms different from the AGPL.
