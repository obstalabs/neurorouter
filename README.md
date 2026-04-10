# NeuroRouter

[![CI](https://github.com/obstalabs/neurorouter/actions/workflows/ci.yml/badge.svg)](https://github.com/obstalabs/neurorouter/actions/workflows/ci.yml)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)
[![Status: Stable](https://img.shields.io/badge/Status-Stable-green)](https://github.com/obstalabs/neurorouter/releases/latest)

Cleans AI requests before they hit the model. Removes wasted context, blocks secrets, and preserves native client semantics locally.

> **Status: Stable.** The community edition is feature-complete and receives bug fixes, security patches, and compatibility updates only. New features ship in [NeuroRouter Pro](https://neurorouter.dev/#pricing).

## Install

```bash
brew install obstalabs/tap/neurorouter
```

Or download from [releases](https://github.com/obstalabs/neurorouter/releases/latest).

## Quick start

```bash
# Claude mode
neurorouter proxy --protocol anthropic --target https://api.anthropic.com --api-key env:ANTHROPIC_API_KEY
ANTHROPIC_BASE_URL=http://localhost:4000 claude

# Codex / OpenAI mode
neurorouter proxy --target https://api.openai.com --api-key env:OPENAI_API_KEY
codex -c 'openai_base_url="http://127.0.0.1:4000"'

# See what would be filtered without sending
neurorouter --dry-run
```

By default NeuroRouter listens on `127.0.0.1:4000`. If you really need remote clients, opt in explicitly with `neurorouter --public --listen 0.0.0.0:4000`. On public binds, `/v1/audit` and `/v1/suggestions` stay disabled unless you also pass `--expose-management`.

The community edition exposes one client protocol per instance. Point it at Anthropic and it serves `/v1/messages` for Claude Code, OpenClaw, and any Anthropic-compatible client. Point it at OpenAI-compatible upstreams and it serves the current Responses-native Codex surface. If you need both Claude and Codex at once, run two instances on different ports.

For live Claude or Codex work in the community edition, the safe rule is one proxy instance per live session unless your client can provide a stable session selector on every request. The free binary does not include premium session-healing or cross-session recovery logic.

If your Anthropic-compatible upstream is a custom or local URL such as `http://localhost:8443`, pass `--protocol anthropic` explicitly. Auto mode can infer protocol from well-known provider URLs, but not from a generic localhost target.

For Codex, the recommended setup is a provider profile that makes the wire mode explicit:

```toml
[providers.neurorouter]
name = "NeuroRouter"
base_url = "http://127.0.0.1:4000"
wire_api = "responses"
```

That keeps Codex on the native Responses API path instead of relying on a generic base-URL override.

For the community edition today, Codex is verified with an OpenAI API key. ChatGPT account-auth pass-through is detected and returned with an explicit compatibility error because the upstream rejects it without `api.responses.write`.

The exact tested client/version combinations live in [docs/compatibility.md](docs/compatibility.md). If a client release changes `/models`, compression, websocket transport, or auth behavior, that matrix is the source of truth for what the community binary currently supports.

## What NeuroRouter is

A drop-in local proxy that sits between your AI coding tool and the API. Every request passes through three stages:

**Block secrets** — credentials, tokens, and connection strings are intercepted inline. Detected leaks are flagged with rotation recipes and prevention hooks.

**Strip waste** — six filters remove structural noise: stale file reads, thinking blocks, orphaned tool results, failed retries, duplicate system reminders, and oversized content blocks.

Universal filters such as `oversized_blocks` and `stale_reads` apply across providers. Provider-specific cleanup such as thinking removal, duplicate system handling, and orphaned tool-result repair is selected by adapter in the live proxy path.

**Preserve semantics** — Codex/OpenAI clients can stay on the native Responses wire path when the selected upstream supports it. For simpler text-only requests against Chat Completions targets, NeuroRouter can still fall back to compatibility translation.

Verified in the community edition for OpenAI/Codex Responses mode and Anthropic Messages mode, one protocol surface per instance. See [docs/compatibility.md](docs/compatibility.md) for the current test-backed client-path matrix and current auth boundary.

## Licensing Model

This repository is the self-hosted community edition of NeuroRouter.

It is also the maintenance-focused community edition. By default, this repo accepts bug fixes, security fixes, compatibility updates, tests, docs, packaging, and other work that keeps the free edition healthy. New product features do not land here unless the community boundary is intentionally changed in a tracked work order.

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
- Anthropic Messages passthrough for Claude Code-compatible upstreams
- local config and CLI workflow

## Free vs Pro

Free cleans your requests. Pro keeps you going when things break.

| | Free | Pro ($29/mo) |
|---|---|---|
| Filters (stale reads, thinking, retries, system reminders) | Yes | Yes |
| Secret detection and blocking | Yes | Yes |
| OPS metrics and audit log | Yes | Yes |
| Protocols per instance | One | All (Claude + Codex in one daemon) |
| Session multiplexing | No | Yes — isolated sessions, no cross-contamination |
| Continuity repair (prevent 400s) | No | Yes — broken tool chains fixed before they hit the API |
| Model routing (Opus → Haiku for mechanical work) | No | Yes — automatic, opt-out with one flag |
| Runaway detection and gating | No | Yes — stops budget burn from looping agents |
| Cooldown warning and context rescue | No | Yes — warns at 80/90/95%, auto-saves work |
| Session healing (repair JSONL automatically) | No | Yes — orphan removal, chain repair, no manual tools |
| Reversible secret redaction | No | Yes — secrets replaced outbound, restored inbound |
| Prompt injection detection | No | Yes — warns on suspicious content in tool results |
| Per-project cost attribution | No | Yes — track spend by repo/branch |
| Sensitive path protection | No | Yes — redacts content, teaches model to handle safely |
| Content access control (deny/allow lists) | No | Yes — configurable per-path content policies |

[Get Pro](https://neurorouter.dev/#pricing) | Free is fully functional for single-session use under AGPL.

If a proposed change adds new product capability instead of maintaining the existing community edition, it belongs in `neurorouter-pro` unless the public boundary is explicitly expanded first.

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

For strict isolation across concurrent clients, set `X-Neurorouter-Session` on proxied requests and pass the same value to `neurorouter audit --session ...`, `neurorouter stats --session ...`, or `neurorouter dnd --session ...`. Without an explicit selector, management views use the default local session bucket. For Claude Code and Codex specifically, prefer one proxy instance per live session in the community edition unless you know the client sends a stable selector.

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
