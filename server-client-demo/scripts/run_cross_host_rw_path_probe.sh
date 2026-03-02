#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_SCRIPT="$ROOT_DIR/scripts/deploy_remote_server.sh"
STOP_SCRIPT="$ROOT_DIR/scripts/stop_remote_server.sh"
PROBE_BIN="$ROOT_DIR/bin/rdma-rw-probe"

REMOTE_HOST="${REMOTE_HOST:-10.0.1.2}"
RDMA_PORT="${RDMA_PORT:-10190}"
RDMA_ENDPOINT="${RDMA_ENDPOINT:-http://$REMOTE_HOST:$RDMA_PORT}"
PREPARE_REMOTE_SERVER="${PREPARE_REMOTE_SERVER:-true}"

SMALL_SIZE="${SMALL_SIZE:-32768}"
LARGE_SIZE="${LARGE_SIZE:-131072}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-10s}"
BUCKET="${BUCKET:-bench-bucket}"
KEY_PREFIX="${KEY_PREFIX:-rw-path-probe}"
AWS_RDMA_RW_DIAG="${AWS_RDMA_RW_DIAG:-true}"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/rw-path-probe-$RUN_ID}"

mkdir -p "$ROOT_DIR/bin" "$RESULT_DIR"

if [ "$PREPARE_REMOTE_SERVER" = "true" ]; then
  echo "preparing remote server"
  "$STOP_SCRIPT" || true
  "$DEPLOY_SCRIPT"
fi

echo "building rw probe binary"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=1 go build -tags rdma -o "$PROBE_BIN" ./cmd/rdma-rw-probe
)

run_case() {
  local label="$1"
  local size="$2"
  local log_file="$RESULT_DIR/${label}.log"

  echo
  echo "===== case=$label size=$size endpoint=$RDMA_ENDPOINT ====="
  (
    AWS_RDMA_RW_DIAG="$AWS_RDMA_RW_DIAG" \
    "$PROBE_BIN" \
      --endpoint "$RDMA_ENDPOINT" \
      --bucket "$BUCKET" \
      --key "${KEY_PREFIX}-${label}" \
      --size "$size" \
      --request-timeout "$REQUEST_TIMEOUT"
  ) 2>&1 | tee "$log_file"
}

run_case "small_${SMALL_SIZE}" "$SMALL_SIZE"
run_case "large_${LARGE_SIZE}" "$LARGE_SIZE"

echo
echo "===== summary ====="
for f in "$RESULT_DIR"/*.log; do
  echo "--- $(basename "$f") ---"
  grep -E "rw probe ok|rdma rw send used|rdma rw recv used|rdma rw send fallback" "$f" || true
done

echo
echo "rw path probe completed"
echo "logs saved in: $RESULT_DIR"
