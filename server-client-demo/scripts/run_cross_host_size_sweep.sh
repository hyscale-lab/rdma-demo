#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"

TARGET_RPS="${TARGET_RPS:-100}"
DURATION="${DURATION:-20s}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/size-sweep-$RUN_ID}"

SIZES=(
  "1MB:1048576"
  "4MB:4194304"
  "16MB:16777216"
)

mkdir -p "$RESULT_DIR"

echo "size sweep started"
echo "target_rps=$TARGET_RPS duration=$DURATION"
echo "result_dir=$RESULT_DIR"

for entry in "${SIZES[@]}"; do
  IFS=":" read -r label bytes <<<"$entry"
  log_file="$RESULT_DIR/size_${label}_rps_${TARGET_RPS}.log"

  echo
  echo "===== run object_size=$label ($bytes bytes) ====="
  (
    OBJECT_SIZE="$bytes" \
    TARGET_RPS="$TARGET_RPS" \
    DURATION="$DURATION" \
    "$COMPARE_SCRIPT"
  ) 2>&1 | tee "$log_file"
done

echo
echo "size sweep completed"
echo "logs saved in: $RESULT_DIR"
