#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/rdma-pool-sweep-$RUN_ID}"

OBJECT_SIZE="${OBJECT_SIZE:-65536}"
ITERATIONS="${ITERATIONS:-50000}"
CONCURRENCY="${CONCURRENCY:-140}"
WARMUP="${WARMUP:-100}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-5s}"
RESTART_REMOTE_PER_CASE="${RESTART_REMOTE_PER_CASE:-true}"

mkdir -p "$RESULT_DIR"

echo "rdma pool sweep started"
echo "result_dir=$RESULT_DIR"
echo "iterations=$ITERATIONS concurrency=$CONCURRENCY object_size=$OBJECT_SIZE warmup=$WARMUP request_timeout=$REQUEST_TIMEOUT"

run_case() {
  local label="$1"
  local s3_client_count="$2"
  local shared_pool="$3"
  local max_conns="$4"
  local prepare_remote="$5"

  local log_file="$RESULT_DIR/${label}.log"

  local pool_key=""
  if [ "$shared_pool" = "true" ]; then
    pool_key="rdma-pool-sweep"
  fi

  echo
  echo "===== case=$label ====="
  echo "s3_client_count=$s3_client_count shared_pool=$shared_pool rdma_max_conns_per_host=$max_conns prepare_remote_server=$prepare_remote"

  (
    MODES=rdma \
    PREPARE_REMOTE_SERVER="$prepare_remote" \
    RESTART_REMOTE_BETWEEN_MODES=false \
    ITERATIONS="$ITERATIONS" \
    CONCURRENCY="$CONCURRENCY" \
    OBJECT_SIZE="$OBJECT_SIZE" \
    WARMUP="$WARMUP" \
    REQUEST_TIMEOUT="$REQUEST_TIMEOUT" \
    S3_CLIENT_COUNT="$s3_client_count" \
    RDMA_SHARED_HTTP_POOL="$shared_pool" \
    RDMA_SHARED_HTTP_POOL_KEY="$pool_key" \
    RDMA_MAX_CONNS_PER_HOST="$max_conns" \
    "$COMPARE_SCRIPT"
  ) 2>&1 | tee "$log_file"
}

prepare_remote=true

# single-client baseline
run_case "baseline_c1_shared_false_max0" 1 false 0 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multi-client without shared pool
run_case "multi_c140_shared_false_max0" 140 false 0 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multi-client with shared pool and adaptive max conns
run_case "multi_c140_shared_true_max0" 140 true 0 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multi-client with shared pool and fixed max conns=4
run_case "multi_c140_shared_true_max4" 140 true 4 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multi-client with shared pool and fixed max conns=8
run_case "multi_c140_shared_true_max8" 140 true 8 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multi-client with shared pool and fixed max conns=16
run_case "multi_c140_shared_true_max16" 140 true 16 "$prepare_remote"
if [ "$RESTART_REMOTE_PER_CASE" = "true" ]; then
  prepare_remote=true
else
  prepare_remote=false
fi

# multi-client with shared pool and fixed max conns=32
run_case "multi_c140_shared_true_max32" 140 true 32 "$prepare_remote"

echo
echo "rdma pool sweep completed"
echo "logs saved in: $RESULT_DIR"
