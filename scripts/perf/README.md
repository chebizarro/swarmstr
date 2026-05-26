# Performance Harness

Run local critical-path benchmarks:

```bash
go run ./scripts/perf --json --n 1000 > perf-report.json
```

Covered paths: policy rule evaluation, trajectory JSONL writes, TokenJuice compaction, a deterministic relay subscription dispatch simulation, and optional cold/warm CLI startup sampling via `bench-cli-startup.sh`. Budgets live in `config.json` for CI trend tracking.
