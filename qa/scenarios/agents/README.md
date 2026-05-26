---
coverage_id: QA-AGENTS-001
title: Agent commitment lifecycle extraction
domain: agents
required_features: [commitment-lifecycle]
required_plugins: []
parity_tier: P1
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/commitments/commitments.go
  - type: grep
    path: internal/commitments/commitments.go
    pattern: CheckSessionHistory
    must_find: true
---
## Steps
- Parse an assistant response with follow-up language.
- Track the commitment as pending and evaluate deterministic session-history evidence for fulfillment, breakage, or expiration.

## Expected
- Regex and optional model-extracted commitments enter the lifecycle store.
- Session history updates pending commitments without sleep-based completion checks.
