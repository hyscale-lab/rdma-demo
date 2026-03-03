#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TCP_RUN_SCRIPT="$ROOT_DIR/scripts/run_cross_host_tcp_sdk_demo.sh"
ZCOPY_RUN_SCRIPT="$ROOT_DIR/scripts/run_cross_host_rdma_zcopy_demo.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/tcp-vs-zcopy-rps-sweep-$RUN_ID}"
SUMMARY_CSV="$RESULT_DIR/summary.csv"

RPS_START="${RPS_START:-500}"
RPS_END="${RPS_END:-3000}"
RPS_STEP="${RPS_STEP:-500}"

DURATION="${DURATION:-20s}"
CLIENT_COUNT="${CLIENT_COUNT:-140}"
MEM_SIZE="${MEM_SIZE:-$((32*1024*1024))}"
PUT_SIZE="${PUT_SIZE:-$((256*1024))}"
GET_MAX_SIZE="${GET_MAX_SIZE:-$((512*1024))}"
CONCURRENT_GET="${CONCURRENT_GET:-8}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-10s}"
STATS_INTERVAL="${STATS_INTERVAL:-100ms}"
TCP_OP="${TCP_OP:-put-get}"
TCP_WARMUP="${TCP_WARMUP:-100}"
TCP_PREFILL="${TCP_PREFILL:-$CLIENT_COUNT}"
TCP_OPEN_LOOP_CLIENT_FANOUT="${TCP_OPEN_LOOP_CLIENT_FANOUT:-false}"
ZCOPY_OP="${ZCOPY_OP:-$TCP_OP}"

RUN_PERF="${RUN_PERF:-true}"
PERF_EVENTS="${PERF_EVENTS:-cycles:u,cycles:k,instructions,task-clock,context-switches,cpu-migrations,cache-misses}"
VERIFY_RESULT="${VERIFY_RESULT:-true}"
REDEPLOY_EACH_CASE="${REDEPLOY_EACH_CASE:-true}"

extract_tcp_throughput() {
  local log_file="$1"
  sed -n 's/^throughput: \([^ ]*\) req\/s.*$/\1/p' "$log_file" | head -n1
}

extract_zcopy_throughput() {
  local log_file="$1"
  grep -m1 '^benchmark mode=' "$log_file" | sed -n 's/.*throughput=\([^ ]*\) req\/s.*/\1/p'
}

extract_latency_p95() {
  local log_file="$1"
  grep -m1 '^latency[: ]' "$log_file" | sed -n 's/.*p95=\([^ ]*\).*/\1/p'
}

extract_perf_counter() {
  local log_file="$1"
  local event="$2"
  awk -v event="$event" '$0 ~ ("[[:space:]]" event "[[:space:]]*$") {gsub(/,/, "", $1); print $1; exit}' "$log_file"
}

mkdir -p "$RESULT_DIR"
echo "rps,tcp_throughput,tcp_p95,tcp_cycles_u,tcp_cycles_k,zcopy_throughput,zcopy_p95,zcopy_cycles_u,zcopy_cycles_k" > "$SUMMARY_CSV"

echo "tcp vs zcopy rps sweep started"
echo "result_dir=$RESULT_DIR"
echo "rps_range=${RPS_START}..${RPS_END} step=${RPS_STEP} duration=$DURATION client_count=$CLIENT_COUNT put_size=$PUT_SIZE tcp_op=$TCP_OP zcopy_op=$ZCOPY_OP"

prepare_remote=true
for ((rps=RPS_START; rps<=RPS_END; rps+=RPS_STEP)); do
  tcp_log="$RESULT_DIR/rps_${rps}_tcp.log"
  zcopy_log="$RESULT_DIR/rps_${rps}_zcopy.log"

  echo
  echo "===== run target_rps=$rps duration=$DURATION client_count=$CLIENT_COUNT ====="
  echo "----- tcp -----"
  (
    PREPARE_REMOTE_SERVER="$prepare_remote" \
    ENABLE_RDMA_ZCOPY=true \
    TARGET_RPS="$rps" \
    DURATION="$DURATION" \
    OP="$TCP_OP" \
    S3_CLIENT_COUNT="$CLIENT_COUNT" \
    PREFILL="$TCP_PREFILL" \
    WARMUP="$TCP_WARMUP" \
    OBJECT_SIZE="$PUT_SIZE" \
    KEY_PREFIX="tcp-sdk-rps-${rps}" \
    REQUEST_TIMEOUT="$REQUEST_TIMEOUT" \
    OPEN_LOOP_CLIENT_FANOUT="$TCP_OPEN_LOOP_CLIENT_FANOUT" \
    RUN_PERF="$RUN_PERF" \
    PERF_EVENTS="$PERF_EVENTS" \
    "$TCP_RUN_SCRIPT"
  ) 2>&1 | tee "$tcp_log"

  echo "----- zcopy -----"
  (
    PREPARE_REMOTE_SERVER=false \
    TARGET_RPS="$rps" \
    DURATION="$DURATION" \
    OP="$ZCOPY_OP" \
    CLIENT_COUNT="$CLIENT_COUNT" \
    MEM_SIZE="$MEM_SIZE" \
    PUT_SIZE="$PUT_SIZE" \
    GET_MAX_SIZE="$GET_MAX_SIZE" \
    CONCURRENT_GET="$CONCURRENT_GET" \
    KEY="zcopy-rps-${rps}" \
    REQUEST_TIMEOUT="$REQUEST_TIMEOUT" \
    STATS_INTERVAL="$STATS_INTERVAL" \
    RUN_PERF="$RUN_PERF" \
    PERF_EVENTS="$PERF_EVENTS" \
    VERIFY_RESULT="$VERIFY_RESULT" \
    "$ZCOPY_RUN_SCRIPT"
  ) 2>&1 | tee "$zcopy_log"

  tcp_throughput="$(extract_tcp_throughput "$tcp_log" || true)"
  tcp_p95="$(extract_latency_p95 "$tcp_log" || true)"
  zcopy_throughput="$(extract_zcopy_throughput "$zcopy_log" || true)"
  zcopy_p95="$(extract_latency_p95 "$zcopy_log" || true)"

  tcp_cycles_u="$(extract_perf_counter "$tcp_log" 'cycles:u' || true)"
  tcp_cycles_k="$(extract_perf_counter "$tcp_log" 'cycles:k' || true)"
  zcopy_cycles_u="$(extract_perf_counter "$zcopy_log" 'cycles:u' || true)"
  zcopy_cycles_k="$(extract_perf_counter "$zcopy_log" 'cycles:k' || true)"

  echo "${rps},${tcp_throughput},${tcp_p95},${tcp_cycles_u},${tcp_cycles_k},${zcopy_throughput},${zcopy_p95},${zcopy_cycles_u},${zcopy_cycles_k}" >> "$SUMMARY_CSV"

  if [ "$REDEPLOY_EACH_CASE" = "true" ]; then
    prepare_remote=true
  else
    prepare_remote=false
  fi
done

echo
echo "tcp vs zcopy rps sweep completed"
echo "summary: $SUMMARY_CSV"
echo "logs:    $RESULT_DIR"
