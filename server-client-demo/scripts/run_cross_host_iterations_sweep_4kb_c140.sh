#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"

OBJECT_SIZE="${OBJECT_SIZE:-65536}"
CONCURRENCY="${CONCURRENCY:-140}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/iterations-sweep-4kb-c140-$RUN_ID}"

ITERATION_VALUES=(10000 20000 30000 40000 50000)

mkdir -p "$RESULT_DIR"

echo "iterations sweep started"
echo "object_size=$OBJECT_SIZE concurrency=$CONCURRENCY"
echo "result_dir=$RESULT_DIR"

for iters in "${ITERATION_VALUES[@]}"; do
  log_file="$RESULT_DIR/iters_${iters}_conc_${CONCURRENCY}_size_${OBJECT_SIZE}.log"

  echo
  echo "===== run iterations=$iters concurrency=$CONCURRENCY object_size=$OBJECT_SIZE bytes ====="
  (
    OBJECT_SIZE="$OBJECT_SIZE" \
    CONCURRENCY="$CONCURRENCY" \
    ITERATIONS="$iters" \
    TARGET_RPS=0 \
    "$COMPARE_SCRIPT"
  ) 2>&1 | tee "$log_file"
done

echo
echo "iterations sweep completed"
echo "logs saved in: $RESULT_DIR"
