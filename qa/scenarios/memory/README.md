---
coverage_id: QA-MEMORY-001
title: Link summaries are compact for memory/context assembly
domain: memory
required_features: [link-understanding]
required_plugins: []
parity_tier: P3
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/linkunderstanding/extract.go
  - type: grep
    path: internal/linkunderstanding/extract.go
    pattern: SummarizeContent
    must_find: true
---
## Steps
- Fetch or parse link content.
- Convert page metadata and body text into a compact summary suitable for context or memory entries.

## Expected
- Long HTML content is stripped of tags and summarized deterministically.
- The summary can be assembled into a prompt context block without live network dependencies.
