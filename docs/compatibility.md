# Community Compatibility

This document tracks the client and version combinations that are verified in the public `neurorouter` community edition.

The rule is simple: if a path is marked supported here, there is both repository evidence and at least one concrete validation reference behind the claim. If it is not listed as supported, do not assume it works in the community binary.

## Version Matrix

| Client | Version | Auth Mode | Proxy Setup | Transport Expectations | Outcome | Verified | Evidence |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Codex CLI / Codex Desktop | `0.117.0` | OpenAI API key on proxy | `codex -c 'openai_base_url="http://127.0.0.1:4023"'` with `neurorouter proxy --api-key env:OPENAI_API_KEY` | `GET /models`, zstd request decoding, websocket `/responses`, sticky turn continuity | Supported | 2026-04-01 | commit `07a572e`, tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go), live smoke recorded on `WO-60` |
| Codex CLI / Codex Desktop | `0.117.0` | ChatGPT account auth / client pass-through | `codex -c 'openai_base_url="http://127.0.0.1:4021"'` with `neurorouter proxy --client-auth` | Same protocol surface as above, but upstream must accept account credential for Responses writes | Unsupported in community edition | 2026-03-31 | `WO-61`, explicit compatibility error in runtime, upstream rejects with missing `api.responses.write` |
| Codex CLI | `0.98.0` | OpenAI API key with custom provider profile | explicit `wire_api = "responses"` provider profile | Responses HTTP path without current default-provider websocket expectations | Supported legacy path | 2026-03-31 | manual smoke during `WO-21` investigation, before `0.117.0` default-provider compatibility work |

## Verified Protocol Surface

The following server behaviors are covered by automated tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go):

- `GET /models` with the Codex discovery shape expected by current OpenAI/Codex clients
- `POST /v1/responses` and `POST /responses`
- zstd-compressed Responses requests from built-in Codex/OpenAI client paths
- websocket `/responses` bridging, including upstream websocket reuse and `previous_response_id` continuity
- compatibility translation from incoming Responses requests to chat-completions upstreams when the selected target does not support native Responses

## Known Unsupported Or Unverified Paths

- Anthropic Messages clients that require inbound `POST /v1/messages`
- Generic OpenAI chat-completions clients that require inbound `POST /v1/chat/completions`
- Tool-specific integrations that need custom headers, auth flows, or non-Responses wire protocols
- Codex ChatGPT account-auth pass-through to OpenAI upstreams. The community proxy returns an explicit compatibility error telling users to use an OpenAI API key instead of failing with an opaque upstream `401`.

Those paths may exist in private development or future work, but they are not part of the verified public community surface today.

## Upgrade Notes

- Current Codex uses `openai_base_url` in config or `-c` overrides. Older `OPENAI_BASE_URL` environment usage is deprecated in recent releases.
- Modern Codex default-provider usage assumes a richer protocol surface than basic HTTP POST alone: `/models`, zstd request decoding, and websocket `/responses` all matter.
- When Codex or OpenAI changes protocol behavior, update this matrix and the evidence references as part of the compatibility WO instead of burying the result in ad hoc notes.

## Operational Rule

Keep this file aligned with the real server surface.

- If we expose a new public client entrypoint in the community edition, add tests first and then add a versioned row here.
- If a client family is not covered by tests and a concrete smoke record, keep it out of the supported matrix and out of quick-start claims.
