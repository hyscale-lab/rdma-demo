#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"

OBJECT_SIZE="${OBJECT_SIZE:-65536}"
DURATION="${DURATION:-20s}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/rps-sweep-64kb-$RUN_ID}"

RPS_VALUES=(1000 2000 3000 4000 5000 6000 7000 8000 9000 10000)

mkdir -p "$RESULT_DIR"

echo "rps sweep started"
echo "object_size=$OBJECT_SIZE duration=$DURATION"
echo "result_dir=$RESULT_DIR"

for rps in "${RPS_VALUES[@]}"; do
  log_file="$RESULT_DIR/rps_${rps}_size_${OBJECT_SIZE}.log"

  echo
  echo "===== run target_rps=$rps object_size=$OBJECT_SIZE bytes ====="
  (
    OBJECT_SIZE="$OBJECT_SIZE" \
    TARGET_RPS="$rps" \
    DURATION="$DURATION" \
    "$COMPARE_SCRIPT"
  ) 2>&1 | tee "$log_file"
done

echo
echo "rps sweep completed"
echo "logs saved in: $RESULT_DIR"
