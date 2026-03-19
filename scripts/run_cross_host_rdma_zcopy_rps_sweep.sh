#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_SCRIPT="$ROOT_DIR/scripts/run_cross_host_rdma_zcopy_demo.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/zcopy-rps-sweep-$RUN_ID}"

RPS_START="${RPS_START:-500}"
RPS_END="${RPS_END:-3000}"
RPS_STEP="${RPS_STEP:-500}"

DURATION="${DURATION:-20s}"
CLIENT_COUNT="${CLIENT_COUNT:-140}"
MEM_SIZE="${MEM_SIZE:-$((32*1024*1024))}"
CONCURRENT_GET="${CONCURRENT_GET:-8}"
PUT_SIZE="${PUT_SIZE:-$((256*1024))}"
GET_MAX_SIZE="${GET_MAX_SIZE:-$((512*1024))}"
STATS_INTERVAL="${STATS_INTERVAL:-100ms}"
RUN_PERF="${RUN_PERF:-true}"
VERIFY_RESULT="${VERIFY_RESULT:-true}"

REDEPLOY_EACH_CASE="${REDEPLOY_EACH_CASE:-false}"

mkdir -p "$RESULT_DIR"

echo "rdma zcopy rps sweep started"
echo "result_dir=$RESULT_DIR"
echo "rps_range=${RPS_START}..${RPS_END} step=${RPS_STEP} duration=$DURATION client_count=$CLIENT_COUNT mem_size=$MEM_SIZE"

prepare_remote=true
for ((rps=RPS_START; rps<=RPS_END; rps+=RPS_STEP)); do
  log_file="$RESULT_DIR/rps_${rps}.log"
  echo
  echo "===== run target_rps=$rps duration=$DURATION client_count=$CLIENT_COUNT ====="

  (
    PREPARE_REMOTE_SERVER="$prepare_remote" \
    TARGET_RPS="$rps" \
    DURATION="$DURATION" \
    CLIENT_COUNT="$CLIENT_COUNT" \
    MEM_SIZE="$MEM_SIZE" \
    CONCURRENT_GET="$CONCURRENT_GET" \
    PUT_SIZE="$PUT_SIZE" \
    GET_MAX_SIZE="$GET_MAX_SIZE" \
    STATS_INTERVAL="$STATS_INTERVAL" \
    RUN_PERF="$RUN_PERF" \
    VERIFY_RESULT="$VERIFY_RESULT" \
    "$RUN_SCRIPT"
  ) 2>&1 | tee "$log_file"

  if [ "$REDEPLOY_EACH_CASE" = "true" ]; then
    prepare_remote=true
  else
    prepare_remote=false
  fi
done

echo
echo "rdma zcopy rps sweep completed"
echo "logs saved in: $RESULT_DIR"
