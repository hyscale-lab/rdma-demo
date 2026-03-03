#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
ZCOPY_BIN="$BIN_DIR/s3-rdma-zcopy-demo-rdma"

REMOTE="${REMOTE:-Liquidz@10.0.1.2}"
TCP_LISTEN="${TCP_LISTEN:-10.0.1.2:10090}"
RDMA_ZCOPY_LISTEN="${RDMA_ZCOPY_LISTEN:-10.0.1.2:10191}"

BUCKET="${BUCKET:-bench-bucket}"
KEY="${KEY:-zcopy-demo}"
OP="${OP:-get}"
MEM_SIZE="${MEM_SIZE:-$((8*1024*1024))}"
PUT_OFFSET="${PUT_OFFSET:-0}"
PUT_SIZE="${PUT_SIZE:-$((256*1024))}"
GET_OFFSET="${GET_OFFSET:-$((1024*1024))}"
GET_MAX_SIZE="${GET_MAX_SIZE:-$((512*1024))}"
CONCURRENT_GET="${CONCURRENT_GET:-8}"
CLIENT_COUNT="${CLIENT_COUNT:-1}"
STATS_INTERVAL="${STATS_INTERVAL:-1s}"
TARGET_RPS="${TARGET_RPS:-0}"
DURATION="${DURATION:-0s}"
VERIFY_RESULT="${VERIFY_RESULT:-true}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-10s}"
RUN_PERF="${RUN_PERF:-true}"
PERF_EVENTS="${PERF_EVENTS:-cycles:u,cycles:k,instructions,task-clock,context-switches,cpu-migrations,cache-misses}"
PREPARE_REMOTE_SERVER="${PREPARE_REMOTE_SERVER:-true}"

if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ] && [ "$DURATION" = "0s" ]; then
  echo "DURATION must be set when TARGET_RPS is enabled (example: DURATION=20s)" >&2
  exit 1
fi

if [ "$PREPARE_REMOTE_SERVER" = "true" ]; then
  echo "deploy remote server with rdma-zcopy enabled"
  (
    cd "$ROOT_DIR"
    ./scripts/stop_remote_server.sh || true
    ENABLE_RDMA_ZCOPY=true \
    RDMA_ZCOPY_LISTEN="$RDMA_ZCOPY_LISTEN" \
    TCP_LISTEN="$TCP_LISTEN" \
    REMOTE="$REMOTE" \
    ./scripts/deploy_remote_server.sh
  )
else
  echo "skip remote deploy (PREPARE_REMOTE_SERVER=false)"
fi

echo
echo "run local zcopy demo"
(
  cd "$ROOT_DIR"
  echo "building zcopy demo client"
  mkdir -p "$BIN_DIR"
  CGO_ENABLED=1 go build -tags rdma -o "$ZCOPY_BIN" ./cmd/s3-rdma-zcopy-demo

  cmd=(
    "$ZCOPY_BIN"
    --endpoint "$RDMA_ZCOPY_LISTEN" \
    --bucket "$BUCKET" \
    --key "$KEY" \
    --op "$OP" \
    --mem-size "$MEM_SIZE" \
    --put-offset "$PUT_OFFSET" \
    --put-size "$PUT_SIZE" \
    --get-offset "$GET_OFFSET" \
    --get-max-size "$GET_MAX_SIZE" \
    --concurrent-get "$CONCURRENT_GET" \
    --client-count "$CLIENT_COUNT" \
    --stats-interval "$STATS_INTERVAL" \
    --request-timeout "$REQUEST_TIMEOUT" \
    --verify-result="$VERIFY_RESULT"
  )

  if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ]; then
    cmd+=(--target-rps "$TARGET_RPS" --duration "$DURATION")
  fi

  if [ "$RUN_PERF" = "true" ] && command -v perf >/dev/null 2>&1; then
    echo "perf_events=$PERF_EVENTS"
    AWS_RDMA_RW_DIAG="${AWS_RDMA_RW_DIAG:-false}" \
    CGO_ENABLED=1 perf stat -e "$PERF_EVENTS" "${cmd[@]}"
  else
    AWS_RDMA_RW_DIAG="${AWS_RDMA_RW_DIAG:-false}" \
    CGO_ENABLED=1 "${cmd[@]}"
  fi
)
