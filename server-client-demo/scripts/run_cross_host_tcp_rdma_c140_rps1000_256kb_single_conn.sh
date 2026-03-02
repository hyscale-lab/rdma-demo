#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPARE_SCRIPT="$ROOT_DIR/scripts/compare_cross_host.sh"
RW_PROBE_SCRIPT="$ROOT_DIR/scripts/run_cross_host_rw_path_probe.sh"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
RESULT_DIR="${RESULT_DIR:-$ROOT_DIR/results/tcp-rdma-c140-rps1000-256kb-single-conn-$RUN_ID}"
LOG_FILE="$RESULT_DIR/compare.log"

# Use scenario-prefixed knobs so accidental shell env (for example OBJECT_SIZE=1024)
# does not silently change this script's intended workload.
SCENARIO_TARGET_RPS="${SCENARIO_TARGET_RPS:-1000}"
SCENARIO_DURATION="${SCENARIO_DURATION:-20s}"
SCENARIO_OBJECT_SIZE="${SCENARIO_OBJECT_SIZE:-262144}"
SCENARIO_CLIENT_COUNT="${SCENARIO_CLIENT_COUNT:-140}"
SCENARIO_OPEN_LOOP_FANOUT="${SCENARIO_OPEN_LOOP_FANOUT:-true}"
SCENARIO_WARMUP="${SCENARIO_WARMUP:-280}"
# Keep tail wait bounded under overloaded single-connection runs.
SCENARIO_REQUEST_TIMEOUT="${SCENARIO_REQUEST_TIMEOUT:-5s}"

# Keep one RDMA connection shared by all S3 clients.
RDMA_SHARED_HTTP_POOL="${RDMA_SHARED_HTTP_POOL:-true}"
RDMA_MAX_CONNS_PER_HOST="${RDMA_MAX_CONNS_PER_HOST:-2048}"
RDMA_ALLOW_FALLBACK="${RDMA_ALLOW_FALLBACK:-false}"

VERIFY_RW_PATH="${VERIFY_RW_PATH:-true}"
AWS_RDMA_RW_DIAG_BENCH="${AWS_RDMA_RW_DIAG_BENCH:-false}"

mkdir -p "$RESULT_DIR"

if [ -n "${OBJECT_SIZE:-}" ] || [ -n "${TARGET_RPS:-}" ] || [ -n "${DURATION:-}" ]; then
  echo "note: OBJECT_SIZE/TARGET_RPS/DURATION env vars are ignored by this script; use SCENARIO_OBJECT_SIZE/SCENARIO_TARGET_RPS/SCENARIO_DURATION."
fi
echo "scenario: tcp+rdma compare, c140, target_rps=$SCENARIO_TARGET_RPS, object_size=$SCENARIO_OBJECT_SIZE, rdma_max_conns=$RDMA_MAX_CONNS_PER_HOST"
echo "note: rdma open-loop run can exceed duration by up to request-timeout while draining in-flight requests (request_timeout=$SCENARIO_REQUEST_TIMEOUT)"
echo "result_dir=$RESULT_DIR"

if [ "$VERIFY_RW_PATH" = "true" ]; then
  echo
  echo "===== verify rw data path before benchmark ====="
  (
    AWS_RDMA_RW_DIAG=true \
    "$RW_PROBE_SCRIPT"
  ) 2>&1 | tee "$RESULT_DIR/rw_probe.log"
fi

echo
echo "===== run benchmark (tcp then rdma) ====="
(
  MODES=both \
  PREPARE_REMOTE_SERVER=true \
  RESTART_REMOTE_BETWEEN_MODES=true \
  TARGET_RPS="$SCENARIO_TARGET_RPS" \
  DURATION="$SCENARIO_DURATION" \
  OBJECT_SIZE="$SCENARIO_OBJECT_SIZE" \
  OP=put-get \
  WARMUP="$SCENARIO_WARMUP" \
  REQUEST_TIMEOUT="$SCENARIO_REQUEST_TIMEOUT" \
  S3_CLIENT_COUNT="$SCENARIO_CLIENT_COUNT" \
  OPEN_LOOP_CLIENT_FANOUT="$SCENARIO_OPEN_LOOP_FANOUT" \
  RDMA_ALLOW_FALLBACK="$RDMA_ALLOW_FALLBACK" \
  RDMA_SHARED_HTTP_POOL="$RDMA_SHARED_HTTP_POOL" \
  RDMA_MAX_CONNS_PER_HOST="$RDMA_MAX_CONNS_PER_HOST" \
  AWS_RDMA_RW_DIAG="$AWS_RDMA_RW_DIAG_BENCH" \
  "$COMPARE_SCRIPT"
) 2>&1 | tee "$LOG_FILE"

echo
echo "===== summary ====="
grep -E "===== (tcp|rdma) =====|mode=|throughput:|latency:|fallback_dial_attempts=|rdma rw send used|rdma rw recv used|rdma rw send fallback" "$LOG_FILE" || true
echo
echo "logs:"
echo "  compare: $LOG_FILE"
if [ -f "$RESULT_DIR/rw_probe.log" ]; then
  echo "  rw_probe: $RESULT_DIR/rw_probe.log"
fi
