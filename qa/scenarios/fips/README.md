---
coverage_id: QA-FIPS-001
title: Fips scenario pack smoke
domain: fips
required_features: []
required_plugins: []
parity_tier: P1
lane: deterministic
pstf: []
checks:
  - type: metadata_only
---
## Steps
- Load this scenario pack and validate required metadata.
- Keep checks deterministic; do not wait on wall-clock sleeps or live relay delivery.

## Expected
- The runner accepts the scenario metadata and reports a passing deterministic check.
