#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TCP_RUN_SCRIPT="$ROOT_DIR/scripts/run_cross_host_tcp_sdk_demo.sh"
ZCOPY_RUN_SCRIPT="$ROOT_DIR/scripts/run_cross_host_rdma_zcopy_demo.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/tcp-vs-zcopy-payload-sweep-$RUN_ID}"
SUMMARY_CSV="$RESULT_DIR/summary.csv"

PAYLOAD_SIZES="${PAYLOAD_SIZES:-10 1000 10000 50000 200000 1000000 2500000 5000000 7500000 10000000 15000000}"

TARGET_RPS="${TARGET_RPS:-200}"
DURATION="${DURATION:-20s}"
CLIENT_COUNT="${CLIENT_COUNT:-140}"
MEM_SIZE="${MEM_SIZE:-$((32*1024*1024))}"
PUT_OFFSET="${PUT_OFFSET:-0}"
GET_OFFSET="${GET_OFFSET:-$((1024*1024))}"
CONCURRENT_GET="${CONCURRENT_GET:-1}"
REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-10s}"
STATS_INTERVAL="${STATS_INTERVAL:-100ms}"
TCP_OP="${TCP_OP:-put-get}"
TCP_WARMUP="${TCP_WARMUP:-100}"
TCP_PREFILL="${TCP_PREFILL:-$CLIENT_COUNT}"
TCP_OPEN_LOOP_CLIENT_FANOUT="${TCP_OPEN_LOOP_CLIENT_FANOUT:-false}"
ZCOPY_OP="${ZCOPY_OP:-$TCP_OP}"

RUN_PERF="${RUN_PERF:-true}"
PERF_EVENTS="${PERF_EVENTS:-cycles:u,cycles:k,instructions,task-clock,context-switches,cpu-migrations,cache-misses}"
VERIFY_RESULT="${VERIFY_RESULT:-false}"
REDEPLOY_EACH_CASE="${REDEPLOY_EACH_CASE:-true}"
STOP_ON_CASE_ERROR="${STOP_ON_CASE_ERROR:-false}"
REDEPLOY_BEFORE_ZCOPY="${REDEPLOY_BEFORE_ZCOPY:-true}"

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

max_payload=0
for size in $PAYLOAD_SIZES; do
  if ! [[ "$size" =~ ^[0-9]+$ ]]; then
    echo "invalid payload size: $size" >&2
    exit 1
  fi
  if [ "$size" -gt "$max_payload" ]; then
    max_payload="$size"
  fi
done

if [ $((PUT_OFFSET + max_payload)) -gt "$MEM_SIZE" ]; then
  echo "invalid memory layout: PUT_OFFSET + max_payload > MEM_SIZE" >&2
  echo "PUT_OFFSET=$PUT_OFFSET max_payload=$max_payload MEM_SIZE=$MEM_SIZE" >&2
  exit 1
fi
if [ $((GET_OFFSET + max_payload)) -gt "$MEM_SIZE" ]; then
  echo "invalid memory layout: GET_OFFSET + max_payload > MEM_SIZE" >&2
  echo "GET_OFFSET=$GET_OFFSET max_payload=$max_payload MEM_SIZE=$MEM_SIZE" >&2
  exit 1
fi

mkdir -p "$RESULT_DIR"
echo "payload_size,tcp_status,tcp_throughput,tcp_p95,tcp_cycles_u,tcp_cycles_k,tcp_instructions,zcopy_status,zcopy_throughput,zcopy_p95,zcopy_cycles_u,zcopy_cycles_k,zcopy_instructions" > "$SUMMARY_CSV"

echo "tcp vs zcopy payload sweep started"
echo "result_dir=$RESULT_DIR"
echo "payload_sizes=$PAYLOAD_SIZES target_rps=$TARGET_RPS duration=$DURATION client_count=$CLIENT_COUNT"

prepare_remote=true
for size in $PAYLOAD_SIZES; do
  tcp_log="$RESULT_DIR/size_${size}_tcp.log"
  zcopy_log="$RESULT_DIR/size_${size}_zcopy.log"

  echo
  echo "===== run payload_size=$size target_rps=$TARGET_RPS duration=$DURATION client_count=$CLIENT_COUNT ====="
  echo "----- tcp -----"
  if (
    PREPARE_REMOTE_SERVER="$prepare_remote" \
    ENABLE_RDMA_ZCOPY=true \
    TARGET_RPS="$TARGET_RPS" \
    DURATION="$DURATION" \
    OP="$TCP_OP" \
    S3_CLIENT_COUNT="$CLIENT_COUNT" \
    PREFILL="$TCP_PREFILL" \
    WARMUP="$TCP_WARMUP" \
    OBJECT_SIZE="$size" \
    KEY_PREFIX="tcp-sdk-size-${size}" \
    REQUEST_TIMEOUT="$REQUEST_TIMEOUT" \
    OPEN_LOOP_CLIENT_FANOUT="$TCP_OPEN_LOOP_CLIENT_FANOUT" \
    RUN_PERF="$RUN_PERF" \
    PERF_EVENTS="$PERF_EVENTS" \
    "$TCP_RUN_SCRIPT"
  ) 2>&1 | tee "$tcp_log"; then
    tcp_status="ok"
  else
    tcp_status="failed"
    echo "warning: tcp case failed payload_size=$size" >&2
    if [ "$STOP_ON_CASE_ERROR" = "true" ]; then
      exit 1
    fi
  fi

  echo "----- zcopy -----"
  zcopy_prepare=false
  if [ "$REDEPLOY_BEFORE_ZCOPY" = "true" ]; then
    zcopy_prepare=true
  fi
  if (
    PREPARE_REMOTE_SERVER="$zcopy_prepare" \
    TARGET_RPS="$TARGET_RPS" \
    DURATION="$DURATION" \
    OP="$ZCOPY_OP" \
    CLIENT_COUNT="$CLIENT_COUNT" \
    MEM_SIZE="$MEM_SIZE" \
    PUT_OFFSET="$PUT_OFFSET" \
    PUT_SIZE="$size" \
    GET_OFFSET="$GET_OFFSET" \
    GET_MAX_SIZE="$size" \
    CONCURRENT_GET="$CONCURRENT_GET" \
    KEY="zcopy-size-${size}" \
    REQUEST_TIMEOUT="$REQUEST_TIMEOUT" \
    STATS_INTERVAL="$STATS_INTERVAL" \
    RUN_PERF="$RUN_PERF" \
    PERF_EVENTS="$PERF_EVENTS" \
    VERIFY_RESULT="$VERIFY_RESULT" \
    "$ZCOPY_RUN_SCRIPT"
  ) 2>&1 | tee "$zcopy_log"; then
    zcopy_status="ok"
  else
    zcopy_status="failed"
    echo "warning: zcopy case failed payload_size=$size" >&2
    if [ "$STOP_ON_CASE_ERROR" = "true" ]; then
      exit 1
    fi
  fi

  tcp_throughput="$(extract_tcp_throughput "$tcp_log" || true)"
  tcp_p95="$(extract_latency_p95 "$tcp_log" || true)"
  zcopy_throughput="$(extract_zcopy_throughput "$zcopy_log" || true)"
  zcopy_p95="$(extract_latency_p95 "$zcopy_log" || true)"

  tcp_cycles_u="$(extract_perf_counter "$tcp_log" 'cycles:u' || true)"
  tcp_cycles_k="$(extract_perf_counter "$tcp_log" 'cycles:k' || true)"
  tcp_instructions="$(extract_perf_counter "$tcp_log" 'instructions' || true)"
  zcopy_cycles_u="$(extract_perf_counter "$zcopy_log" 'cycles:u' || true)"
  zcopy_cycles_k="$(extract_perf_counter "$zcopy_log" 'cycles:k' || true)"
  zcopy_instructions="$(extract_perf_counter "$zcopy_log" 'instructions' || true)"

  echo "${size},${tcp_status},${tcp_throughput},${tcp_p95},${tcp_cycles_u},${tcp_cycles_k},${tcp_instructions},${zcopy_status},${zcopy_throughput},${zcopy_p95},${zcopy_cycles_u},${zcopy_cycles_k},${zcopy_instructions}" >> "$SUMMARY_CSV"

  if [ "$REDEPLOY_EACH_CASE" = "true" ]; then
    prepare_remote=true
  else
    prepare_remote=false
  fi
done

echo
echo "tcp vs zcopy payload sweep completed"
echo "summary: $SUMMARY_CSV"
echo "logs:    $RESULT_DIR"
