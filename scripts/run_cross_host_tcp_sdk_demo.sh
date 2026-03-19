#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
BENCH_BIN="$BIN_DIR/bench-client"

REMOTE="${REMOTE:-Liquidz@10.0.1.2}"
TCP_LISTEN="${TCP_LISTEN:-10.0.1.2:10090}"
ENABLE_RDMA_ZCOPY="${ENABLE_RDMA_ZCOPY:-false}"
RDMA_ZCOPY_LISTEN="${RDMA_ZCOPY_LISTEN:-10.0.1.2:10191}"

BUCKET="${BUCKET:-bench-bucket}"
KEY_PREFIX="${KEY_PREFIX:-tcp-sdk-demo}"
OBJECT_SIZE="${OBJECT_SIZE:-$((256*1024))}"
OP="${OP:-put}"
WARMUP="${WARMUP:-100}"
PREFILL="${PREFILL:-0}"
S3_CLIENT_COUNT="${S3_CLIENT_COUNT:-140}"
OPEN_LOOP_CLIENT_FANOUT="${OPEN_LOOP_CLIENT_FANOUT:-false}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-10s}"
PAYLOAD_ROOT="${PAYLOAD_ROOT:-}"
CONTROL_GRPC_LISTEN="${CONTROL_GRPC_LISTEN:-}"

TARGET_RPS="${TARGET_RPS:-500}"
DURATION="${DURATION:-20s}"
ITERATIONS="${ITERATIONS:-1000}"
CONCURRENCY="${CONCURRENCY:-64}"

RUN_PERF="${RUN_PERF:-true}"
PERF_EVENTS="${PERF_EVENTS:-cycles:u,cycles:k,instructions,task-clock,context-switches,cpu-migrations,cache-misses}"
PREPARE_REMOTE_SERVER="${PREPARE_REMOTE_SERVER:-true}"

if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ] && [ "$DURATION" = "0s" ]; then
  echo "DURATION must be set when TARGET_RPS is enabled (example: DURATION=20s)" >&2
  exit 1
fi

if [ "$OP" = "get" ] && [ "$PREPARE_REMOTE_SERVER" = "true" ] && [ -z "$PAYLOAD_ROOT" ]; then
  echo "OP=get requires readable payload objects on the server." >&2
  echo "Set PAYLOAD_ROOT for deploy-time preload, or preload payloads separately before running with PREPARE_REMOTE_SERVER=false." >&2
  exit 1
fi

if [ "$PREPARE_REMOTE_SERVER" = "true" ]; then
  echo "deploy remote server for tcp sdk benchmark"
  (
    cd "$ROOT_DIR"
    ./scripts/stop_remote_server.sh || true
    ENABLE_RDMA_ZCOPY="$ENABLE_RDMA_ZCOPY" \
    RDMA_ZCOPY_LISTEN="$RDMA_ZCOPY_LISTEN" \
    TCP_LISTEN="$TCP_LISTEN" \
    PAYLOAD_ROOT="$PAYLOAD_ROOT" \
    CONTROL_GRPC_LISTEN="$CONTROL_GRPC_LISTEN" \
    REMOTE="$REMOTE" \
    ./scripts/deploy_remote_server.sh
  )
else
  echo "skip remote deploy (PREPARE_REMOTE_SERVER=false)"
fi

echo
echo "run local tcp sdk benchmark"
(
  cd "$ROOT_DIR"
  echo "building bench client"
  mkdir -p "$BIN_DIR"
  CGO_ENABLED=1 go build -tags rdma -o "$BENCH_BIN" ./cmd/bench-client

  if [ "$OP" = "get" ]; then
    echo "note: GET mode expects preloaded payload objects matching ${KEY_PREFIX}-00000000 .."
  fi

  cmd=(
    "$BENCH_BIN"
    --mode tcp \
    --endpoint "http://$TCP_LISTEN" \
    --op "$OP" \
    --object-size "$OBJECT_SIZE" \
    --bucket "$BUCKET" \
    --key-prefix "$KEY_PREFIX" \
    --warmup "$WARMUP" \
    --request-timeout "$REQUEST_TIMEOUT" \
    --s3-client-count "$S3_CLIENT_COUNT" \
    --open-loop-client-fanout="$OPEN_LOOP_CLIENT_FANOUT"
  )

  if [ "$PREFILL" -gt 0 ]; then
    cmd+=(--prefill "$PREFILL")
  fi

  if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ]; then
    cmd+=(--target-rps "$TARGET_RPS" --duration "$DURATION")
  else
    cmd+=(--iterations "$ITERATIONS" --concurrency "$CONCURRENCY")
  fi

  if [ "$RUN_PERF" = "true" ] && command -v perf >/dev/null 2>&1; then
    echo "perf_events=$PERF_EVENTS"
    CGO_ENABLED=1 perf stat -e "$PERF_EVENTS" "${cmd[@]}"
  else
    CGO_ENABLED=1 "${cmd[@]}"
  fi
)
