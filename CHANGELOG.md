# Changelog

## [Unreleased]

## [0.1.34] - 2026-04-20

### Changed
- Reframed community-edition docs and CLI messaging around basic context hygiene instead of raw token savings, while keeping the free/pro capability boundary unchanged

## [0.1.33] - 2026-04-07

### Fixed
- Removed `orphaned_results` from the Claude filter chain so Anthropic sessions keep tool-result turns intact instead of self-triggering tool-use concurrency `400` errors
- Kept OpenAI/Codex orphan cleanup unchanged so Responses compatibility and compaction safety stay intact

## [0.1.32] - 2026-04-05

### Fixed
- `stale_reads` now preserves distinct read requests for the same file within each write-delimited segment and only drops earlier exact duplicates, preventing long Claude sessions from spiraling into re-read loops after the proxy strips too much file context
- Responses websocket bridge state now releases explicit-session state after disconnect with an idle-grace reuse window, stops leaking fallback per-connection session references, and no longer holds bridge state mutexes across upstream websocket or HTTP fallback I/O

## [0.1.31] - 2026-04-05

### Fixed
- Codex translation streaming now uses a 2MB scanner buffer and reports `scanner.Err()` instead of silently truncating oversized SSE events
- Proxy handlers now recover panics into structured local `502` responses instead of dropping connections without an error body
- Anthropic and Codex streaming forwards now stop reading upstream when the downstream client disconnects or a stream write fails, preventing silent retry-driven budget burn
- Non-streaming passthrough paths now log copy and response-write failures instead of discarding them silently

## [0.1.30] - 2026-04-05

### Fixed
- Ported the Anthropic rewrite body-size guard from Pro so the proxy now keeps the original raw request whenever cleanup reserialization would still make the forwarded body larger because of HTML escaping or key reordering
- Added regression coverage proving rewritten Anthropic requests fall back to the original body unchanged instead of surfacing `+bytes` in proxy logs
- Anthropic rewrite now compacts filtered request JSON before forwarding, preventing small positive byte deltas after reminder cleanup on the current unreleased head
- Anthropic rewrite now merges adjacent same-role messages introduced by filtered message removal, preserving valid role alternation instead of surfacing rewrite failures
- Remaining Anthropic rewrite validation failures now return local `400` responses instead of `500`, so clients stop retrying malformed rewritten requests

## [0.1.29] - 2026-04-04

### Fixed
- Claude continuation-history cleanup now deduplicates repeated auto-compaction summaries so only the latest equivalent continuation summary survives while distinct summaries remain intact
- Claude stale-read cleanup now also removes repeated structured shell transcript chains when the same shell command and output are replayed again, keeping only the latest equivalent transcript
- Claude failed-retry cleanup now drops superseded failed bash and PowerShell tool results once a later equivalent shell attempt succeeds, while unresolved failures and distinct commands remain intact

### Changed
- Compatibility docs now distinguish direct upstream Responses websocket relay, which preserves the native websocket envelope, from the HTTP fallback bridge, which strips websocket-only request fields while keeping continuity fields intact

## [0.1.28] - 2026-04-04

### Fixed
- Anthropic rewrite hardening now strips empty text blocks on every marshal path instead of only one filtered-content branch, so future cleanup changes cannot reintroduce blank-content `400` failures through alternate content encodings
- Anthropic rewrite validation now detects same-role adjacency introduced by message removal while allowing pre-existing upstream adjacency to pass through unchanged, preventing future filter changes from silently producing invalid user/user or assistant/assistant sequences

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
