#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
BENCH_BIN="$BIN_DIR/bench-client"

TCP_ENDPOINT="${TCP_ENDPOINT:-http://127.0.0.1:9000}"
RDMA_ENDPOINT="${RDMA_ENDPOINT:-http://127.0.0.1:19090}"
ITERATIONS="${ITERATIONS:-10000}"
CONCURRENCY="${CONCURRENCY:-64}"
OBJECT_SIZE="${OBJECT_SIZE:-4096}"
OP="${OP:-put-get}"
BUCKET="${BUCKET:-bench-bucket}"
GO_BUILD_FLAGS="${GO_BUILD_FLAGS:--tags rdma}"
RDMA_ALLOW_FALLBACK="${RDMA_ALLOW_FALLBACK:-false}"

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
    --iterations "$ITERATIONS"
    --concurrency "$CONCURRENCY"
    --object-size "$OBJECT_SIZE"
    --bucket "$BUCKET"
    "$@"
  )

  echo
  echo "===== $mode ====="
  echo "endpoint=$endpoint iterations=$ITERATIONS concurrency=$CONCURRENCY object_size=$OBJECT_SIZE op=$OP"

  if command -v perf >/dev/null 2>&1; then
    if perf stat \
      -e cycles,instructions,task-clock,context-switches,cpu-migrations,cache-misses \
      "${cmd[@]}"; then
      return
    fi
    echo "perf stat failed, falling back to direct run"
    "${cmd[@]}"
  else
    "${cmd[@]}"
  fi
}

run_case tcp "$TCP_ENDPOINT"
run_case rdma "$RDMA_ENDPOINT" --allow-fallback="$RDMA_ALLOW_FALLBACK"
