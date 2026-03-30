# Changelog

## [0.1.0] - 2026-03-29

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
