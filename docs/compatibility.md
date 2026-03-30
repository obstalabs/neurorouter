# Community Compatibility

This document tracks the client paths that are verified in the public `neurorouter` community edition.

The rule is simple: if a client path is marked verified here, there is test coverage in this repository for the corresponding HTTP entrypoint. If it is not listed as verified, do not assume it works in the community binary.

## Verified in this repository

- Codex and other Responses-native clients using `POST /v1/responses`
- Codex-compatible clients using `POST /responses`
- Compatibility translation from incoming Responses requests to chat-completions upstreams when the selected target does not support native Responses

These paths are covered by automated tests in [internal/neurorouter/proxy_test.go](../internal/neurorouter/proxy_test.go).

## Not verified in the community edition

- Anthropic Messages clients that require `POST /v1/messages`
- Generic OpenAI chat-completions clients that require inbound `POST /v1/chat/completions`
- Tool-specific integrations that need custom headers, auth flows, or non-Responses wire protocols

Those paths may exist in private development or future work, but they are not part of the verified public community surface today.

## Operational rule

Keep this file aligned with the real server surface.

- If we expose a new public client entrypoint in the community edition, add tests first and then move the path into the verified section.
- If a client family is not covered by tests, keep it out of the verified section and out of quick-start claims.
