---
coverage_id: QA-CHANNELS-001
title: Channel messages include link understanding context
domain: channels
required_features: [link-understanding]
required_plugins: []
parity_tier: P2
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/linkunderstanding/extract.go
  - type: grep
    path: internal/linkunderstanding/extract.go
    pattern: ExtractURLs
    must_find: true
---
## Steps
- Accept an inbound channel message containing one or more HTTP(S) links.
- Extract URLs and render fetched metadata into prompt context.

## Expected
- Link extraction de-duplicates URLs and trims punctuation.
- Context assembly includes title, description, summary, and final URL metadata.
