#!/usr/bin/env bash
set -euo pipefail

# Capture production-safe daemon turn span logs emitted as:
#   daemon_turn_span {"event":"daemon_turn_span",...}
#
# Usage options:
#   LOG_FILE=/path/to/metiqd.log scripts/perf/bench-daemon-turn.sh
#   METIQD_CMD="./metiqd" WORKLOAD_CMD="./metiq chat.send ..." scripts/perf/bench-daemon-turn.sh
#
# Run the same wrapper in Docker and bare-metal; the instrumentation is identical.

LOG_FILE=${LOG_FILE:-}
METIQD_CMD=${METIQD_CMD:-}
WORKLOAD_CMD=${WORKLOAD_CMD:-}
OUT=${OUT:-}
WAIT_SECS=${WAIT_SECS:-2}

cleanup() {
  if [[ -n "${daemon_pid:-}" ]]; then
    kill "$daemon_pid" >/dev/null 2>&1 || true
    wait "$daemon_pid" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ -z "$LOG_FILE" ]]; then
  if [[ -z "$METIQD_CMD" || -z "$WORKLOAD_CMD" ]]; then
    echo "set LOG_FILE, or set both METIQD_CMD and WORKLOAD_CMD" >&2
    exit 2
  fi
  tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/metiq-turn-perf.XXXXXX")
  LOG_FILE="$tmpdir/metiqd.log"
  # shellcheck disable=SC2086
  $METIQD_CMD >"$LOG_FILE" 2>&1 &
  daemon_pid=$!
  sleep "$WAIT_SECS"
  # shellcheck disable=SC2086
  $WORKLOAD_CMD >/dev/null
  sleep "$WAIT_SECS"
fi

python3 - "$LOG_FILE" "$OUT" <<'PY'
import json
import re
import statistics
import sys
from collections import defaultdict
from pathlib import Path

log_path = Path(sys.argv[1])
out_path = Path(sys.argv[2]) if len(sys.argv) > 2 and sys.argv[2] else None
pattern = re.compile(r'daemon_turn_span\s+(\{.*\})')
spans = []
for line in log_path.read_text(errors="replace").splitlines():
    match = pattern.search(line)
    if not match:
        continue
    try:
        event = json.loads(match.group(1))
    except json.JSONDecodeError:
        continue
    if event.get("event") == "daemon_turn_span":
        spans.append(event)

by_category = defaultdict(list)
for span in spans:
    category = span.get("category") or "unknown"
    duration = span.get("duration_ms")
    if isinstance(duration, (int, float)):
        by_category[category].append(duration)

summary = {}
for category, values in sorted(by_category.items()):
    ordered = sorted(values)
    summary[category] = {
        "count": len(values),
        "min_ms": ordered[0],
        "median_ms": statistics.median(ordered),
        "max_ms": ordered[-1],
    }

recall = [s for s in spans if s.get("category") == "memory_recall_query"]
duplicate_recall_queries = [s for s in recall if s.get("duplicate_query_this_turn")]
report = {
    "benchmark": "daemon_turn_path",
    "log_file": str(log_path),
    "span_count": len(spans),
    "summary": summary,
    "duplicate_recall_query_count": len(duplicate_recall_queries),
    "duplicate_recall_queries_observed": bool(duplicate_recall_queries),
    "spans": spans,
}
raw = json.dumps(report, indent=2, sort_keys=True)
if out_path:
    out_path.write_text(raw + "\n")
print(raw)
PY
