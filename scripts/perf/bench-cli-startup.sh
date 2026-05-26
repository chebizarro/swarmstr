#!/usr/bin/env bash
set -euo pipefail
BIN=${BIN:-./metiq}
N=${N:-5}
if [[ ! -x "$BIN" ]]; then
  echo "binary $BIN is not executable; build metiq first or set BIN=/path/to/metiq" >&2
  exit 2
fi
run_once() {
  local start end
  start=$(python3 - <<'PY'
import time; print(time.perf_counter_ns())
PY
)
  "$BIN" version >/dev/null
  end=$(python3 - <<'PY'
import time; print(time.perf_counter_ns())
PY
)
  echo $((end-start))
}
echo '{"benchmark":"cli_startup","samples_ns":['
for i in $(seq 1 "$N"); do
  sample=$(run_once)
  if [[ "$i" != "1" ]]; then printf ',\n'; fi
  printf '%s' "$sample"
done
echo ']}'
