#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
TCP_BIN="$BIN_DIR/s3-tcp-demo"

REMOTE="${REMOTE:-Liquidz@10.0.1.2}"
TCP_LISTEN="${TCP_LISTEN:-10.0.1.2:10090}"
ENABLE_RDMA_ZCOPY="${ENABLE_RDMA_ZCOPY:-false}"
RDMA_ZCOPY_LISTEN="${RDMA_ZCOPY_LISTEN:-10.0.1.2:10191}"

BUCKET="${BUCKET:-bench-bucket}"
KEY_PREFIX="${KEY_PREFIX:-tcp-demo}"
OBJECT_SIZE="${OBJECT_SIZE:-$((256*1024))}"
CLIENT_COUNT="${CLIENT_COUNT:-140}"
TARGET_RPS="${TARGET_RPS:-500}"
DURATION="${DURATION:-20s}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-10s}"
STATS_INTERVAL="${STATS_INTERVAL:-1s}"
VERIFY_RESULT="${VERIFY_RESULT:-true}"
MAX_CONNS_PER_HOST="${MAX_CONNS_PER_HOST:-1}"
RUN_PERF="${RUN_PERF:-true}"
PERF_EVENTS="${PERF_EVENTS:-cycles:u,cycles:k,instructions,task-clock,context-switches,cpu-migrations,cache-misses}"
PREPARE_REMOTE_SERVER="${PREPARE_REMOTE_SERVER:-true}"

if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ] && [ "$DURATION" = "0s" ]; then
  echo "DURATION must be set when TARGET_RPS is enabled (example: DURATION=20s)" >&2
  exit 1
fi

if [ "$PREPARE_REMOTE_SERVER" = "true" ]; then
  echo "deploy remote server for tcp benchmark"
  (
    cd "$ROOT_DIR"
    ./scripts/stop_remote_server.sh || true
    ENABLE_RDMA_ZCOPY="$ENABLE_RDMA_ZCOPY" \
    RDMA_ZCOPY_LISTEN="$RDMA_ZCOPY_LISTEN" \
    TCP_LISTEN="$TCP_LISTEN" \
    REMOTE="$REMOTE" \
    ./scripts/deploy_remote_server.sh
  )
else
  echo "skip remote deploy (PREPARE_REMOTE_SERVER=false)"
fi

echo
echo "run local tcp demo"
(
  cd "$ROOT_DIR"
  echo "building tcp demo client"
  mkdir -p "$BIN_DIR"
  CGO_ENABLED=1 go build -o "$TCP_BIN" ./cmd/s3-tcp-demo

  cmd=(
    "$TCP_BIN"
    --endpoint "http://$TCP_LISTEN" \
    --bucket "$BUCKET" \
    --key-prefix "$KEY_PREFIX" \
    --object-size "$OBJECT_SIZE" \
    --client-count "$CLIENT_COUNT" \
    --request-timeout "$REQUEST_TIMEOUT" \
    --stats-interval "$STATS_INTERVAL" \
    --max-conns-per-host "$MAX_CONNS_PER_HOST" \
    --verify-result="$VERIFY_RESULT"
  )

  if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ]; then
    cmd+=(--target-rps "$TARGET_RPS" --duration "$DURATION")
  fi

  if [ "$RUN_PERF" = "true" ] && command -v perf >/dev/null 2>&1; then
    echo "perf_events=$PERF_EVENTS"
    CGO_ENABLED=1 perf stat -e "$PERF_EVENTS" "${cmd[@]}"
  else
    CGO_ENABLED=1 "${cmd[@]}"
  fi
)
