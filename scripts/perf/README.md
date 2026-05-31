# Performance Harness

Run local critical-path benchmarks:

```bash
go run ./scripts/perf --json --n 1000 > perf-report.json
```

Covered paths: policy rule evaluation, trajectory JSONL writes, TokenJuice compaction, a deterministic relay subscription dispatch simulation, daemon turn span capture via `bench-daemon-turn.sh`, and optional cold/warm CLI startup sampling via `bench-cli-startup.sh`. Budgets live in `config.json` for CI trend tracking.

Daemon turn baselines use production structured logs:

```bash
LOG_FILE=~/.metiq/metiqd.log scripts/perf/bench-daemon-turn.sh > daemon-turn-baseline.json
# or run a daemon plus synthetic workload command with the same wrapper:
METIQD_CMD="./metiqd" WORKLOAD_CMD="./metiq chat.send --session perf --message 'hello'" scripts/perf/bench-daemon-turn.sh
```

Run the same wrapper against Docker logs and bare-metal logs to compare `/data` named-volume and `~/.metiq` filesystem costs.
