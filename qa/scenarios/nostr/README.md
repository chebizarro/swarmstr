---
coverage_id: QA-NOSTR-001
title: Nostr commitment sync contract
domain: nostr
required_features: [nostr-adoption-contracts, commitments]
required_plugins: []
parity_tier: P1
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/nostr/adoption/contracts.go
  - type: grep
    path: internal/nostr/adoption/contracts.go
    pattern: CommitmentEventFromTracked
    must_find: true
---
## Steps
- Load the Nostr adoption contracts.
- Verify tracked commitments can be converted into kind 30384 commitment sync draft events without polling or request/response semantics.

## Expected
- Commitment sync remains represented as a Nostr event contract.
- The scenario passes using static deterministic checks only.
