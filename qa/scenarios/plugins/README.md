---
coverage_id: QA-PLUGINS-001
title: Model catalog supports provider indexing
domain: plugins
required_features: [model-catalog]
required_plugins: []
parity_tier: P2
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/catalog/catalog.go
  - type: grep
    path: internal/catalog/catalog.go
    pattern: ByProvider
    must_find: true
---
## Steps
- Register built-in and configured provider models in the catalog registry.
- Query the provider index and search by capabilities.

## Expected
- The registry lists models independently from daemon internals.
- Provider and capability filters return deterministic model sets for plugin/UI use.
