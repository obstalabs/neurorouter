# Trust Architecture

NeuroRouter earns trust by making betrayal technically difficult, not by promising good behavior.

## Five Pillars

### 1. Local-first

NeuroRouter runs on your machine. Your API key never leaves your network. This eliminates 90% of trust concerns — there is no server to breach, no database to leak, no third party to subpoena.

```
Your machine                    Upstream provider
┌──────────────────┐           ┌──────────────────┐
│  Your app        │           │  OpenAI / Groq / │
│       ↓          │           │  DeepSeek / etc  │
│  NeuroRouter     │──────────→│                  │
│  (localhost:4000)│  HTTPS    │                  │
└──────────────────┘           └──────────────────┘
```

Nothing leaves your machine except the locally prepared request over HTTPS to your chosen upstream.

### 2. No key inspection

The Authorization header is forwarded at the HTTP transport layer. NeuroRouter never parses, decodes, stores, or logs it.

The proxy operates on message content (`[]ChatMessage`), not on HTTP headers. The key passthrough happens in a single line of code:

```go
upReq.Header.Set("Authorization", "Bearer "+target.APIKey)
```

The key value is set once in config and used only for the outbound request. It is never included in audit logs, suggestions, dry-run output, or any other data structure.

### 3. No outbound calls

The binary makes exactly one type of network call: forwarding the locally prepared request to the user's configured upstream endpoint.

For Responses-native clients such as Codex, NeuroRouter preserves structured items like tool outputs and reasoning envelopes when the upstream supports them. Local filtering and protection only rewrite the text-bearing message parts of the request.

There is:
- Zero telemetry
- Zero analytics
- Zero phone-home
- Zero background update checks
- Zero online license validation

You can verify this with network monitoring:

```bash
# Only connections should be to your configured upstream
lsof -i -P | grep neurorouter
```

### 4. Deterministic transformation

Every filter and protection rule is deterministic. No ML, no confidence scores, no probabilistic decisions.

- Filters use exact pattern matching (regex on serialized JSON patterns)
- Secret detection uses structural patterns (prefix-based, not heuristic)
- Every transformation is logged: what was removed, which filter, how many bytes

The `/v1/audit` endpoint shows a complete record of all transformations:

```json
{
  "entries": [
    {
      "timestamp": "2026-03-28T14:00:00Z",
      "model": "gpt-4o",
      "bytes_before": 12400,
      "bytes_after": 8200,
      "bytes_removed": 4200,
      "filters_run": ["thinking", "system_reminders"],
      "secrets_found": 0
    }
  ]
}
```

### 5. Verifiable

Users can verify every claim without trusting documentation:

**Dry-run mode** — see exactly what would be filtered without sending anything upstream:

```bash
neurorouter --dry-run --target https://api.openai.com
```

Returns the original and filtered request side by side:

```json
{
  "original": [{"role": "user", "content": "...original..."}],
  "filtered": [{"role": "user", "content": "...filtered..."}],
  "bytes_before": 12400,
  "bytes_after": 8200,
  "bytes_removed": 4200,
  "filters_run": ["thinking", "system_reminders"]
}
```

**Audit log** — review all transformations after the fact:

```bash
curl http://localhost:4000/v1/audit
```

**Network monitoring** — confirm no unexpected connections:

```bash
lsof -i -P | grep neurorouter
# Should show ONLY your configured upstream, nothing else
```

## What NeuroRouter Does NOT Do

- Does not store API keys (forwards them, never saves them)
- Does not log request or response content (only transformation metadata)
- Does not make outbound network calls beyond forwarding to configured upstream
- Does not use ML or probabilistic methods for any decision
- Does not persist data to disk (session-scoped memory only)
- Does not require an account, registration, or authentication to use
- Does not phone home, check for updates, or validate licenses at runtime

## The Self-Hosted Escape Hatch

Don't trust us? Run it yourself.

```bash
go build ./cmd/neurorouter
./neurorouter --target https://api.openai.com --api-key env:OPENAI_API_KEY
```

The community edition source in this repository is licensed under the AGPL. Read every line. Build from source. Run it on your own machine. The trust model is designed so that you never need to trust us blindly — you can verify everything yourself.
