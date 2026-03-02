#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
BENCH_BIN="$BIN_DIR/bench-client"

TCP_ENDPOINT="${TCP_ENDPOINT:-http://127.0.0.1:10090}"
RDMA_ENDPOINT="${RDMA_ENDPOINT:-http://127.0.0.1:10190}"
ITERATIONS="${ITERATIONS:-10000}"
CONCURRENCY="${CONCURRENCY:-64}"
TARGET_RPS="${TARGET_RPS:-0}"
DURATION="${DURATION:-0s}"
OBJECT_SIZE="${OBJECT_SIZE:-4096}"
OP="${OP:-put-get}"
BUCKET="${BUCKET:-bench-bucket}"
GO_BUILD_FLAGS="${GO_BUILD_FLAGS:--tags rdma}"
RDMA_ALLOW_FALLBACK="${RDMA_ALLOW_FALLBACK:-false}"
RDMA_FRAME_PAYLOAD="${RDMA_FRAME_PAYLOAD:-0}"
RDMA_SEND_DEPTH="${RDMA_SEND_DEPTH:-0}"
RDMA_RECV_DEPTH="${RDMA_RECV_DEPTH:-0}"
RDMA_INLINE_THRESHOLD="${RDMA_INLINE_THRESHOLD:-0}"
RDMA_SEND_SIGNAL_INTERVAL="${RDMA_SEND_SIGNAL_INTERVAL:-0}"
RDMA_LOWCPU="${RDMA_LOWCPU:-false}"
MODES="${MODES:-both}"
PERF_EVENTS="${PERF_EVENTS:-cycles:u,cycles:k,instructions,task-clock,context-switches,cpu-migrations,cache-misses}"
WARMUP="${WARMUP:-100}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-5s}"
S3_CLIENT_COUNT="${S3_CLIENT_COUNT:-1}"
OPEN_LOOP_CLIENT_FANOUT="${OPEN_LOOP_CLIENT_FANOUT:-false}"
RDMA_MAX_CONNS_PER_HOST="${RDMA_MAX_CONNS_PER_HOST:-0}"
RDMA_SHARED_HTTP_POOL="${RDMA_SHARED_HTTP_POOL:-false}"
RDMA_SHARED_HTTP_POOL_KEY="${RDMA_SHARED_HTTP_POOL_KEY:-}"
RDMA_ENDPOINT_POOL_SIZE="${RDMA_ENDPOINT_POOL_SIZE:-0}"
RDMA_ENDPOINT_POOL_WARMUP="${RDMA_ENDPOINT_POOL_WARMUP:-false}"
RDMA_SHARED_MEMORY_BUDGET_BYTES="${RDMA_SHARED_MEMORY_BUDGET_BYTES:-0}"
RDMA_ENDPOINT_MULTIPLEX="${RDMA_ENDPOINT_MULTIPLEX:-false}"
RDMA_ENDPOINT_SEND_QUEUE_DEPTH="${RDMA_ENDPOINT_SEND_QUEUE_DEPTH:-0}"

if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ] && [ "$DURATION" = "0s" ]; then
  echo "DURATION must be set when TARGET_RPS is enabled (example: DURATION=60s)" >&2
  exit 1
fi

if [ "$MODES" != "both" ] && [ "$MODES" != "tcp" ] && [ "$MODES" != "rdma" ]; then
  echo "MODES must be one of: both, tcp, rdma" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"

echo "building bench client"
(
  cd "$ROOT_DIR"
  read -r -a BUILD_FLAGS_ARR <<<"$GO_BUILD_FLAGS"
  go build "${BUILD_FLAGS_ARR[@]}" -o "$BENCH_BIN" ./cmd/bench-client
)

run_case() {
  local mode="$1"
  local endpoint="$2"
  shift 2

  local cmd=(
    "$BENCH_BIN"
    --mode "$mode"
    --endpoint "$endpoint"
    --op "$OP"
    --object-size "$OBJECT_SIZE"
    --bucket "$BUCKET"
    --warmup "$WARMUP"
    --request-timeout "$REQUEST_TIMEOUT"
    --s3-client-count "$S3_CLIENT_COUNT"
    --open-loop-client-fanout="$OPEN_LOOP_CLIENT_FANOUT"
    "$@"
  )

  if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ]; then
    cmd+=(--target-rps "$TARGET_RPS" --duration "$DURATION")
  else
    cmd+=(--iterations "$ITERATIONS" --concurrency "$CONCURRENCY")
  fi

  echo
  echo "===== $mode ====="
  if [ "$TARGET_RPS" != "0" ] && [ "$TARGET_RPS" != "0.0" ]; then
    echo "endpoint=$endpoint target_rps=$TARGET_RPS duration=$DURATION object_size=$OBJECT_SIZE op=$OP request_timeout=$REQUEST_TIMEOUT client_count=$S3_CLIENT_COUNT open_loop_client_fanout=$OPEN_LOOP_CLIENT_FANOUT"
  else
    echo "endpoint=$endpoint iterations=$ITERATIONS concurrency=$CONCURRENCY object_size=$OBJECT_SIZE op=$OP request_timeout=$REQUEST_TIMEOUT client_count=$S3_CLIENT_COUNT"
  fi

  if command -v perf >/dev/null 2>&1; then
    echo "perf_events=$PERF_EVENTS"
    if perf stat \
      -e "$PERF_EVENTS" \
      "${cmd[@]}"; then
      return
    fi
    echo "perf stat failed, falling back to direct run"
    "${cmd[@]}"
  else
    "${cmd[@]}"
  fi
}

if [ "$MODES" = "both" ] || [ "$MODES" = "tcp" ]; then
  run_case tcp "$TCP_ENDPOINT"
fi
if [ "$MODES" = "both" ] || [ "$MODES" = "rdma" ]; then
  rdma_extra_args=(
    --allow-fallback="$RDMA_ALLOW_FALLBACK"
    --rdma-frame-payload "$RDMA_FRAME_PAYLOAD"
    --rdma-send-depth "$RDMA_SEND_DEPTH"
    --rdma-recv-depth "$RDMA_RECV_DEPTH"
    --rdma-inline-threshold "$RDMA_INLINE_THRESHOLD"
    --rdma-send-signal-interval "$RDMA_SEND_SIGNAL_INTERVAL"
    --rdma-lowcpu="$RDMA_LOWCPU"
    --rdma-max-conns-per-host "$RDMA_MAX_CONNS_PER_HOST"
    --rdma-shared-http-pool="$RDMA_SHARED_HTTP_POOL"
    --rdma-endpoint-pool-size "$RDMA_ENDPOINT_POOL_SIZE"
    --rdma-endpoint-pool-warmup="$RDMA_ENDPOINT_POOL_WARMUP"
    --rdma-shared-memory-budget-bytes "$RDMA_SHARED_MEMORY_BUDGET_BYTES"
    --rdma-endpoint-multiplex="$RDMA_ENDPOINT_MULTIPLEX"
    --rdma-endpoint-send-queue-depth "$RDMA_ENDPOINT_SEND_QUEUE_DEPTH"
  )

  if [ -n "$RDMA_SHARED_HTTP_POOL_KEY" ]; then
    rdma_extra_args+=(--rdma-shared-http-pool-key "$RDMA_SHARED_HTTP_POOL_KEY")
  fi

  run_case rdma "$RDMA_ENDPOINT" "${rdma_extra_args[@]}"
fi
