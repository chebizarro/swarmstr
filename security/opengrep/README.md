# Metiq OpenGrep Rulepack

Static-analysis rules for architecture and Nostr protocol violations introduced by parity/adoption work.

Run with OpenGrep/Semgrep-compatible tooling:

```bash
opengrep scan --config security/opengrep/metiq-nostr.yml .
```

The rulepack focuses on PR-diff blocking signals: polling for Nostr delivery, timeout-based completion, broad filters, ignored relay responses, missing event validation, RPC/queue abstractions over Nostr, unsafe secret handling, and plugin/security boundary smells.
