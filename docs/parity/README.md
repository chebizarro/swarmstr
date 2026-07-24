# OpenClaw parity catalogs

The checked-in gateway and CLI snapshots are generated from OpenClaw's static
descriptor catalogs:

- `gateway-method-parity.json` — advertised gateway methods plus Metiq's
  current implementation status and Phase 1 triage.
- `cli-parity.json` — top-level core/sub-CLI descriptors plus explicit Metiq
  classifications.

The human-maintained inputs are:

- `gateway-triage.json` — one classification and rationale for every gateway
  method group.
- `cli-classifications.json` — one classification for every CLI descriptor.
- `core-deviations.json` and `control-transport-deviations.json` — accepted
  architectural deviations.

Refresh from an OpenClaw checkout:

```bash
OPENCLAW_ROOT=/path/to/openclaw bash scripts/refresh-parity.sh
```

Verify that the checked-in output matches that checkout:

```bash
OPENCLAW_ROOT=/path/to/openclaw bash scripts/refresh-parity.sh --check
```

The generator records the OpenClaw commit, commit date, and descriptor hashes.
It fails if a new descriptor lacks triage/classification or if a stale
classification no longer matches a descriptor. The Go verifier reads the docs
gateway snapshot directly; there is no verifier fixture copy to synchronize.
