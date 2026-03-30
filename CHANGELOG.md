# Changelog

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
