# Changelog

## [0.1.27] - 2026-04-04

### Fixed
- Anthropic message cleanup now strips empty text blocks after system-reminder filtering on structured mixed-content messages, so Claude no longer receives invalid requests that fail with `400` when a reminder-only text block is paired with retained `tool_result` blocks

## [0.1.26] - 2026-04-03

### Changed
- Clarified the community-edition session boundary for Claude Code and Codex, including the safe operational rule of one proxy instance per live session unless the client can provide a stable session selector

## [0.1.25] - 2026-04-03

### Fixed
- Claude `/v1/messages` cleanup now removes stale `Read` chains at block level instead of dropping whole mixed tool turns, so valid `tool_use` and `tool_result` pairs survive while stale reads are stripped
- Large retained Claude `Read` tool results are now shaped deterministically under cleanup instead of passing through whole once they become the dominant remaining byte sink
- Runtime secret diagnostics now ignore stripped Claude `thinking.signature` payloads, so Anthropic cleanup sessions no longer report those internal signatures as forwarded high-entropy secrets

## [0.1.24] - 2026-04-03

### Fixed
- Anthropic Messages cleanup now rewrites text-only content block arrays semantically instead of flattening filtered JSON back into quoted strings, so Claude filtering no longer reports positive byte growth on thinking and system-reminder cleanup paths

## [0.1.23] - 2026-04-03

### Added
- Added an Anthropic Messages passthrough mode so a free NeuroRouter instance can front Claude clients directly via `/v1/messages` while staying pinned to a single protocol surface per instance

### Fixed
- Removed leaked `ppiankov` identity references from the free repo module path, imports, and CLA metadata after the public repo transfer
- Removed the stale `stripe-go` dependency from the free repo module graph

## [0.1.22] - 2026-04-03

### Fixed
- Native Responses replay cleanup now preserves assistant `output_text` history on compact and resume paths while still normalizing user, system, and developer replay text to `input_text`, so Codex sessions can continue through NeuroRouter without schema errors on replayed assistant turns

## [0.1.21] - 2026-04-01

### Fixed
- Native Responses cleanup now trims large read-style `function_call_output` and `custom_tool_call_output` transcripts under `oversized_blocks`, so Codex file-read and code-inspection transcripts no longer pass through untouched when they dominate request size

## [0.1.20] - 2026-04-01

### Fixed
- Added native `POST /responses/compact` and `POST /v1/responses/compact` support so Codex `0.118.0` compaction requests no longer fail with a local `404`

### Changed
- Updated the compatibility matrix to record verified Codex `0.118.0` support on the OpenAI API-key path, including the compact endpoint requirement

## [0.1.19] - 2026-04-01

### Fixed
- Native Responses no-op rewrites now preserve the original raw request body, so formatting-only re-marshaling no longer shows fake positive byte growth like `+2 bytes`

## [0.1.8] - 2026-04-01

### Fixed
- Claude `tool_result` cleanup now parses structured bash and PowerShell JSON payloads so oversized shell transcripts are slimmed semantically while preserving decisive metadata like exit interpretation and background state

## [0.1.7] - 2026-04-01

### Fixed
- Native Responses cleanup now removes duplicate shell transcript chains and superseded failed shell retries, so stale Codex shell history is stripped before it reaches the upstream

### Changed
- Added an opt-in `--shell-max-output-bytes` cap that truncates oversized native shell outputs while preserving the tail error or success signal
- Per-request proxy logging now shows signed byte deltas so small savings are visible instead of rounding away into `0% saved`

## [0.1.6] - 2026-04-01

### Fixed
- Native Responses cleanup now strips stale structured Codex/OpenAI tool items, including repeated read/output pairs and orphaned structured outputs, so large duplicated request bodies are removed before they hit the upstream

### Changed
- Added replay and proxy coverage that proves Codex-style native Responses cleanup can remove more than 100KB of waste while preserving valid tool continuity

## [0.1.5] - 2026-04-01

### Changed
- Repointed release metadata, README install guidance, and update checks to the `obstalabs` repository and package feeds for the ownership transfer

## [0.1.4] - 2026-04-01

### Fixed
- Current Codex default-provider flows now work end-to-end through NeuroRouter with `/models` compatibility, zstd request decoding, websocket `/responses`, and preserved turn continuity on the supported OpenAI API-key path

### Changed
- Added a versioned client compatibility matrix in `docs/compatibility.md` and linked it from the README so tested Codex/OpenAI paths and current auth limits are explicit

## [0.1.3] - 2026-03-31

### Fixed
- Explicit upstream targets no longer borrow credentials from a different provider during startup auto-detection
- Proxy startup now shows exported auth env keys and supports an explicit `--client-auth` mode for pass-through Authorization

## [0.1.2] - 2026-03-31

### Changed
- Clarified proxy startup hints so the banner reports the active auth mode and prints client guidance that matches the detected upstream
- Added an explicit Claude quick start example alongside the Responses-compatible Codex setup in the README

## [0.1.1] - 2026-03-31

### Fixed
- Release workflow now skips Homebrew and Scoop publishing when their tokens are absent instead of failing the entire tagged release
- GoReleaser uses a separate Scoop token configuration instead of incorrectly reusing the Homebrew tap token

## [0.1.0] - 2026-03-31

### Added
- Translation proxy: Responses API to Chat Completions with streaming support
- Content filters: stale reads, thinking blocks, orphaned results, failed retries, system reminders, oversized blocks
- Secret detection: 16+ credential patterns with block/redact/warn policies
- Rotation recipes: 15 credential types with step-by-step remediation
- Multi-provider filter adapters: Claude, OpenAI, generic Chat Completions
- Provider interface and registry for multi-provider routing
- Neurocache: pattern detection with severity, cost, and install actions
- Workflow detection: recurring tool call sequence identification
- DND mode: frustration detection with auto-expiry
- Progress tracker: OPS, savings, and pattern metrics
- Inline alert injection: tiered messages with throttling and aggregation
- Audit log and dry-run mode for trust verification
- CLI with Cobra: proxy, stats, explain, audit, dnd, version
- Zero-config first-run with auto-detection from API key env vars

### Fixed
- Default bind stays on loopback and management endpoints remain opt-in on public listens
- Runtime config precedence is wired into proxy startup for supported community settings
- Self-update verifies checksums and installs extracted platform binaries instead of archive blobs
- Live pipeline uses provider-aware adapters and native Responses passthrough for compatible upstreams
- Audit, suggestions, and DND state are isolated per session when clients send a session selector
