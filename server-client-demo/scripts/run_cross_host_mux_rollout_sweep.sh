#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/rdma-mux-rollout-$RUN_ID}"

OBJECT_SIZE="${OBJECT_SIZE:-65536}"
ITERATIONS="${ITERATIONS:-50000}"
CONCURRENCY="${CONCURRENCY:-140}"
WARMUP="${WARMUP:-100}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-5s}"
S3_CLIENT_COUNT="${S3_CLIENT_COUNT:-140}"
RDMA_ENDPOINT_POOL_SIZE="${RDMA_ENDPOINT_POOL_SIZE:-16}"
RDMA_MAX_CONNS_PER_HOST="${RDMA_MAX_CONNS_PER_HOST:-16}"
RDMA_SHARED_HTTP_POOL="${RDMA_SHARED_HTTP_POOL:-true}"
RESTART_REMOTE_PER_CASE="${RESTART_REMOTE_PER_CASE:-true}"

mkdir -p "$RESULT_DIR"

echo "rdma mux rollout sweep started"
echo "result_dir=$RESULT_DIR"
echo "iterations=$ITERATIONS concurrency=$CONCURRENCY object_size=$OBJECT_SIZE warmup=$WARMUP request_timeout=$REQUEST_TIMEOUT"
echo "s3_client_count=$S3_CLIENT_COUNT endpoint_pool_size=$RDMA_ENDPOINT_POOL_SIZE max_conns_per_host=$RDMA_MAX_CONNS_PER_HOST shared_http_pool=$RDMA_SHARED_HTTP_POOL"

run_case() {
  local label="$1"
  local client_mux="$2"
  local server_mux="$3"
  local mux_sendq="$4"
  local prepare_remote="$5"

  local log_file="$RESULT_DIR/${label}.log"

  echo
  echo "===== case=$label ====="
  echo "client_mux=$client_mux server_mux=$server_mux mux_sendq=$mux_sendq prepare_remote_server=$prepare_remote"

  (
    MODES=rdma \
    PREPARE_REMOTE_SERVER="$prepare_remote" \
    RESTART_REMOTE_BETWEEN_MODES=false \
    ITERATIONS="$ITERATIONS" \
    CONCURRENCY="$CONCURRENCY" \
    OBJECT_SIZE="$OBJECT_SIZE" \
    WARMUP="$WARMUP" \
    REQUEST_TIMEOUT="$REQUEST_TIMEOUT" \
    S3_CLIENT_COUNT="$S3_CLIENT_COUNT" \
    RDMA_SHARED_HTTP_POOL="$RDMA_SHARED_HTTP_POOL" \
    RDMA_MAX_CONNS_PER_HOST="$RDMA_MAX_CONNS_PER_HOST" \
    RDMA_ENDPOINT_POOL_SIZE="$RDMA_ENDPOINT_POOL_SIZE" \
    RDMA_ENDPOINT_MULTIPLEX="$client_mux" \
    RDMA_ENDPOINT_SEND_QUEUE_DEPTH="$mux_sendq" \
    RDMA_MULTIPLEX="$server_mux" \
    RDMA_MULTIPLEX_SEND_QUEUE_DEPTH="$mux_sendq" \
    "$COMPARE_SCRIPT"
  ) 2>&1 | tee "$log_file"
}

prepare_remote=true

# old path, no multiplex
run_case "legacy_pool${RDMA_ENDPOINT_POOL_SIZE}" false false 0 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multiplex on, default queue depth
run_case "mux_pool${RDMA_ENDPOINT_POOL_SIZE}_q64" true true 64 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multiplex on, deeper queue depth
run_case "mux_pool${RDMA_ENDPOINT_POOL_SIZE}_q128" true true 128 "$prepare_remote"

echo
echo "rdma mux rollout sweep completed"
echo "logs saved in: $RESULT_DIR"
