# Security Policy

## Trust Model

NeuroRouter runs locally on your machine and listens on loopback by default. Your API keys never leave your network unless you send them to your configured upstream provider.

The proxy forwards the Authorization header as-is at the HTTP layer. It does not parse, store, log, or inspect your API key. NeuroRouter does not need your key to work — it only forwards it.

If you explicitly opt into a public bind, any client that can reach the proxy can use the upstream credentials configured in NeuroRouter. Public binds are therefore opt-in, and management endpoints stay off unless you deliberately expose them.

## Architecture Guarantees

1. **No key inspection**: Authorization header is passed through untouched. The proxy operates on request content while leaving auth handling separate
2. **No outbound calls**: the binary talks only to your configured upstream endpoint. Zero telemetry, zero phone-home
3. **Loopback by default**: the default bind is `127.0.0.1:4000`; non-loopback binds require explicit opt-in
4. **Deterministic transforms**: every filter operation is visible via `--dry-run` and the local `/v1/audit` endpoint
5. **Local-only persistence**: API keys and request content are not written to disk, but the community edition can keep local structural state such as pattern counts and DND/session metadata in `~/.neurorouter/state.db`

## Verifying These Claims

```bash
# See exactly what would be filtered without sending anything
neurorouter --dry-run --target https://api.openai.com

# Monitor network connections (should show ONLY your configured upstream)
lsof -i -P | grep neurorouter

# Review the audit log of all transformations from the local bind
curl http://127.0.0.1:4000/v1/audit
```

## Reporting Vulnerabilities

If you discover a security vulnerability, please report it privately:

1. Do NOT open a public GitHub issue
2. Email the maintainer directly (see git log for contact)
3. Include: description, reproduction steps, impact assessment

We will acknowledge receipt within 48 hours and provide a fix timeline within 7 days.

## Scope

The following are in scope for security reports:
- Key leakage (Authorization header logged, stored, or sent to unintended destination)
- Unexpected outbound connections (binary phones home or contacts non-configured endpoints)
- Filter bypass (secret detection fails to catch a credential pattern)
- Content injection (filtered content is modified in a way that changes meaning beyond removal)

The following are out of scope:
- Denial of service on the local binary
- Issues requiring physical access to the machine running neurorouter
- Vulnerabilities in upstream LLM providers
