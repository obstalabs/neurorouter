# Licensing Strategy

## Community Edition

The source code and self-hosted community binaries in this repository are licensed under the GNU Affero General Public License v3.0. See [LICENSE](../LICENSE) for the full text and [NOTICE](../NOTICE) for copyright and trademark notices.

AGPL requires that if you modify NeuroRouter and make that modified version available to users over a network, you must also make the corresponding source available to those users.

AGPL does not by itself restrict:
- running NeuroRouter internally without distribution
- building tools that talk to NeuroRouter over HTTP
- charging money for support, distribution, or separate commercial licenses

## Commercial Licensing

Obsta Labs, LLC offers commercial licenses for organizations that need terms different from the AGPL.

Commercial terms may cover:
- non-copyleft redistribution terms
- procurement and compliance requirements
- paid support and onboarding
- managed deployment or hosted offerings
- separately licensed add-ons or services

## Feature Boundary

This public repository is the community edition only.

It is also the maintenance-focused community edition. The default rule is simple: preserve and maintain the existing free feature set here, and put new product capability in `neurorouter-pro` unless the public boundary is intentionally expanded first.

Included here under AGPL:
- local proxy runtime
- filtering, protection, audit, dry-run, and DND
- provider adapters, compatibility routing, and Responses passthrough
- local CLI and configuration for the community edition

Kept out of this repo and reserved for paid/private distribution:
- premium model-cascade and task-routing logic
- runaway detection and lockout-warning systems
- context rescue and checkpoint extraction features
- premium session-awareness and spend-risk signals
- team, enterprise, hosted, or control-plane tooling

Important boundary: if code is published in this repository under the AGPL, recipients of that code receive AGPL rights to that code. Commercial terms do not retroactively remove those rights.

Operational rule for maintainers:
- `neurorouter-free` gets fixes, security work, compatibility updates, docs, tests, packaging, and maintenance of the current community feature set
- `neurorouter-pro` gets new premium/private product work unless a tracked work order explicitly changes the public boundary

## What this means for contributors

All contributions require signing the [CLA](CLA.md). The CLA grants rights to Obsta Labs, LLC to distribute contributions under both AGPL and commercial license terms. Contributors retain ownership of their work.

## What we will NOT do

- Add telemetry to enforce licensing
- Mislabel the license of this repository
- Claim proprietary control over code already published here under the AGPL
