#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/rdma-coldstart-check-$RUN_ID}"

ROUNDS="${ROUNDS:-2}"
CASE_LABEL="${CASE_LABEL:-rdma_coldstart_case}"

OBJECT_SIZE="${OBJECT_SIZE:-65536}"
ITERATIONS="${ITERATIONS:-50000}"
CONCURRENCY="${CONCURRENCY:-140}"
WARMUP="${WARMUP:-500}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-5s}"
S3_CLIENT_COUNT="${S3_CLIENT_COUNT:-140}"

RDMA_SHARED_HTTP_POOL="${RDMA_SHARED_HTTP_POOL:-false}"
RDMA_SHARED_HTTP_POOL_KEY="${RDMA_SHARED_HTTP_POOL_KEY:-}"
RDMA_MAX_CONNS_PER_HOST="${RDMA_MAX_CONNS_PER_HOST:-0}"
RDMA_ENDPOINT_POOL_SIZE="${RDMA_ENDPOINT_POOL_SIZE:-0}"
RDMA_ENDPOINT_POOL_WARMUP="${RDMA_ENDPOINT_POOL_WARMUP:-false}"
RDMA_ENDPOINT_MULTIPLEX="${RDMA_ENDPOINT_MULTIPLEX:-false}"
RDMA_ENDPOINT_SEND_QUEUE_DEPTH="${RDMA_ENDPOINT_SEND_QUEUE_DEPTH:-0}"
RDMA_SHARED_MEMORY_BUDGET_BYTES="${RDMA_SHARED_MEMORY_BUDGET_BYTES:-0}"
RDMA_MULTIPLEX="${RDMA_MULTIPLEX:-false}"
RDMA_MULTIPLEX_SEND_QUEUE_DEPTH="${RDMA_MULTIPLEX_SEND_QUEUE_DEPTH:-0}"

if ! [[ "$ROUNDS" =~ ^[0-9]+$ ]] || [ "$ROUNDS" -lt 2 ]; then
  echo "ROUNDS must be an integer >= 2" >&2
  exit 1
fi

mkdir -p "$RESULT_DIR"

echo "rdma coldstart check started"
echo "result_dir=$RESULT_DIR"
echo "rounds=$ROUNDS case_label=$CASE_LABEL"
echo "iterations=$ITERATIONS concurrency=$CONCURRENCY object_size=$OBJECT_SIZE warmup=$WARMUP request_timeout=$REQUEST_TIMEOUT"
echo "client_count=$S3_CLIENT_COUNT shared_pool=$RDMA_SHARED_HTTP_POOL max_conns=$RDMA_MAX_CONNS_PER_HOST endpoint_pool=$RDMA_ENDPOINT_POOL_SIZE endpoint_multiplex=$RDMA_ENDPOINT_MULTIPLEX server_multiplex=$RDMA_MULTIPLEX"

for round in $(seq 1 "$ROUNDS"); do
  if [ "$round" -eq 1 ]; then
    prepare_remote=true
  else
    prepare_remote=false
  fi

  log_file="$RESULT_DIR/${CASE_LABEL}_round${round}.log"

  echo
  echo "===== round=${round}/${ROUNDS} prepare_remote_server=${prepare_remote} ====="

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
    RDMA_SHARED_HTTP_POOL_KEY="$RDMA_SHARED_HTTP_POOL_KEY" \
    RDMA_MAX_CONNS_PER_HOST="$RDMA_MAX_CONNS_PER_HOST" \
    RDMA_ENDPOINT_POOL_SIZE="$RDMA_ENDPOINT_POOL_SIZE" \
    RDMA_ENDPOINT_POOL_WARMUP="$RDMA_ENDPOINT_POOL_WARMUP" \
    RDMA_ENDPOINT_MULTIPLEX="$RDMA_ENDPOINT_MULTIPLEX" \
    RDMA_ENDPOINT_SEND_QUEUE_DEPTH="$RDMA_ENDPOINT_SEND_QUEUE_DEPTH" \
    RDMA_SHARED_MEMORY_BUDGET_BYTES="$RDMA_SHARED_MEMORY_BUDGET_BYTES" \
    RDMA_MULTIPLEX="$RDMA_MULTIPLEX" \
    RDMA_MULTIPLEX_SEND_QUEUE_DEPTH="$RDMA_MULTIPLEX_SEND_QUEUE_DEPTH" \
    "$COMPARE_SCRIPT"
  ) 2>&1 | tee "$log_file"
done

echo
echo "summary (rdma rounds):"
for round in $(seq 1 "$ROUNDS"); do
  log_file="$RESULT_DIR/${CASE_LABEL}_round${round}.log"
  echo "--- round $round ---"
  grep -E "^(success=|throughput:|latency:)" "$log_file" | tail -n 3 || true
done

echo
echo "rdma coldstart check completed"
echo "logs saved in: $RESULT_DIR"
