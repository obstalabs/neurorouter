# Community Compatibility

This document tracks the client and version combinations that are verified in the public `neurorouter` community edition.

The rule is simple: if a path is marked supported here, there is both repository evidence and at least one concrete validation reference behind the claim. If it is not listed as supported, do not assume it works in the community binary.

## Version Matrix

| Client | Version | Auth Mode | Proxy Setup | Transport Expectations | Outcome | Verified | Evidence |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Claude Code | current community path | Anthropic API key on proxy | `ANTHROPIC_BASE_URL=http://127.0.0.1:4000 claude` with `neurorouter proxy --protocol anthropic --target https://api.anthropic.com --api-key env:ANTHROPIC_API_KEY` | `POST /v1/messages`, Anthropic auth headers, single-protocol Anthropic instance, safest as one proxy instance per live Claude session | Supported | 2026-04-03 | tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go) and [cmd/neurorouter/proxy_test.go](../cmd/neurorouter/proxy_test.go), implemented in `WO-102` |
| Codex CLI / Codex Desktop | `0.118.0` | OpenAI API key on proxy | `codex -c 'openai_base_url="http://127.0.0.1:4038"'` with `neurorouter proxy --api-key env:OPENAI_API_KEY` | `GET /models`, zstd request decoding, websocket `/responses`, `POST /responses/compact`, sticky turn continuity | Supported | 2026-04-01 | tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go), live smoke through real OpenAI target on 2026-04-01 |
| Codex CLI / Codex Desktop | `0.117.0` | OpenAI API key on proxy | `codex -c 'openai_base_url="http://127.0.0.1:4023"'` with `neurorouter proxy --api-key env:OPENAI_API_KEY` | `GET /models`, zstd request decoding, websocket `/responses`, sticky turn continuity | Supported | 2026-04-01 | commit `07a572e`, tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go), live smoke recorded on `WO-60` |
| Codex CLI / Codex Desktop | `0.117.0` | ChatGPT account auth / client pass-through | `codex -c 'openai_base_url="http://127.0.0.1:4021"'` with `neurorouter proxy --client-auth` | Same protocol surface as above, but upstream must accept account credential for Responses writes | Unsupported in community edition | 2026-03-31 | `WO-61`, explicit compatibility error in runtime, upstream rejects with missing `api.responses.write` |
| Codex CLI | `0.98.0` | OpenAI API key with custom provider profile | explicit `wire_api = "responses"` provider profile | Responses HTTP path without current default-provider websocket expectations | Supported legacy path | 2026-03-31 | manual smoke during `WO-21` investigation, before `0.117.0` default-provider compatibility work |
| OpenClaw | `2026.3.28` | Anthropic API key (via OpenClaw auth-profiles) | `ANTHROPIC_BASE_URL=http://127.0.0.1:9120` with `neurorouter proxy --protocol anthropic --target https://api.anthropic.com` | `POST /v1/messages`, standard Anthropic auth, streaming, 17 tools, max_tokens=8192 | Supported | 2026-04-05 | live capture on VM, thinking+failed_retries filters verified, 11% noise removed on first request |

## Verified Protocol Surface

The following server behaviors are covered by automated tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go):

- `POST /v1/messages` when the proxy is running in Anthropic mode
- `GET /models` with the Codex discovery shape expected by current OpenAI/Codex clients
- `POST /v1/responses` and `POST /responses`
- `POST /v1/responses/compact` and `POST /responses/compact`
- zstd-compressed Responses requests from built-in Codex/OpenAI client paths
- websocket `/responses` bridging, including direct upstream websocket reuse that preserves the native websocket envelope and `previous_response_id` continuity
- compatibility translation from incoming Responses requests to chat-completions upstreams when the selected target does not support native Responses

## Known Unsupported Or Unverified Paths

- Generic OpenAI chat-completions clients that require inbound `POST /v1/chat/completions`
- Serving both Anthropic Messages and OpenAI Responses from one community-edition instance. Run two proxies on different ports or use the pro multi-client hub path instead.
- Reusing one community-edition proxy instance across unrelated live Claude or Codex sessions when the client does not send a stable session selector. For the free edition, run one instance per live session.
- Tool-specific integrations that need custom headers, auth flows, or non-Responses wire protocols
- Codex ChatGPT account-auth pass-through to OpenAI upstreams. The community proxy returns an explicit compatibility error telling users to use an OpenAI API key instead of failing with an opaque upstream `401`.

Those paths may exist in private development or future work, but they are not part of the verified public community surface today.

## Upgrade Notes

- For Claude and other Anthropic-compatible clients, pass `--protocol anthropic` when the upstream target is a custom or local URL such as `http://localhost:8443`. Auto mode can infer protocol from well-known provider URLs, but not from a generic localhost target.
- In the community edition, dedicate one proxy instance to one live Claude or Codex session unless your client can set a stable session selector such as `X-Neurorouter-Session` on every request.
- Current Codex uses `openai_base_url` in config or `-c` overrides. Older `OPENAI_BASE_URL` environment usage is deprecated in recent releases.
- Modern Codex default-provider usage assumes a richer protocol surface than basic HTTP POST alone: `/models`, zstd request decoding, websocket `/responses`, and newer releases like `0.118.0` also use `/responses/compact`.
- When native websocket reuse is available, NeuroRouter relays the Responses websocket envelope unchanged. If a request falls back to the HTTP bridge path, the proxy strips websocket-only request fields like `type`, `client_metadata`, and `generate` while preserving turn continuity fields such as `previous_response_id` and Codex turn-state headers.
- When Codex or OpenAI changes protocol behavior, update this matrix and the evidence references as part of the compatibility WO instead of burying the result in ad hoc notes.

## Operational Rule

Keep this file aligned with the real server surface.

- If we expose a new public client entrypoint in the community edition, add tests first and then add a versioned row here.
- If a client family is not covered by tests and a concrete smoke record, keep it out of the supported matrix and out of quick-start claims.
