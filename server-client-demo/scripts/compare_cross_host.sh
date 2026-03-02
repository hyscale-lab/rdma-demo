#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REMOTE_HOST="${REMOTE_HOST:-10.0.1.2}"
TCP_PORT="${TCP_PORT:-10090}"
RDMA_PORT="${RDMA_PORT:-10190}"
MODES="${MODES:-both}"
PREPARE_REMOTE_SERVER="${PREPARE_REMOTE_SERVER:-true}"
RESTART_REMOTE_BETWEEN_MODES="${RESTART_REMOTE_BETWEEN_MODES:-true}"

if [ "$PREPARE_REMOTE_SERVER" = "true" ]; then
  echo "preparing remote server: stop old process and start a fresh instance"
  "$ROOT_DIR/scripts/stop_remote_server.sh" || true
  "$ROOT_DIR/scripts/deploy_remote_server.sh"
fi

export TCP_ENDPOINT="${TCP_ENDPOINT:-http://$REMOTE_HOST:$TCP_PORT}"
export RDMA_ENDPOINT="${RDMA_ENDPOINT:-http://$REMOTE_HOST:$RDMA_PORT}"
export GO_BUILD_FLAGS="${GO_BUILD_FLAGS:--tags rdma}"
export RDMA_ALLOW_FALLBACK="${RDMA_ALLOW_FALLBACK:-false}"
export RDMA_FRAME_PAYLOAD="${RDMA_FRAME_PAYLOAD:-0}"
export RDMA_SEND_DEPTH="${RDMA_SEND_DEPTH:-0}"
export RDMA_RECV_DEPTH="${RDMA_RECV_DEPTH:-0}"
export RDMA_INLINE_THRESHOLD="${RDMA_INLINE_THRESHOLD:-0}"
export RDMA_SEND_SIGNAL_INTERVAL="${RDMA_SEND_SIGNAL_INTERVAL:-0}"
export RDMA_LOWCPU="${RDMA_LOWCPU:-false}"
export WARMUP="${WARMUP:-100}"
export S3_CLIENT_COUNT="${S3_CLIENT_COUNT:-1}"
export OPEN_LOOP_CLIENT_FANOUT="${OPEN_LOOP_CLIENT_FANOUT:-false}"
export RDMA_MAX_CONNS_PER_HOST="${RDMA_MAX_CONNS_PER_HOST:-0}"
export RDMA_SHARED_HTTP_POOL="${RDMA_SHARED_HTTP_POOL:-false}"
export RDMA_SHARED_HTTP_POOL_KEY="${RDMA_SHARED_HTTP_POOL_KEY:-}"
export RDMA_ENDPOINT_POOL_SIZE="${RDMA_ENDPOINT_POOL_SIZE:-0}"
export RDMA_ENDPOINT_POOL_WARMUP="${RDMA_ENDPOINT_POOL_WARMUP:-false}"
export RDMA_SHARED_MEMORY_BUDGET_BYTES="${RDMA_SHARED_MEMORY_BUDGET_BYTES:-0}"
export RDMA_ENDPOINT_MULTIPLEX="${RDMA_ENDPOINT_MULTIPLEX:-false}"
export RDMA_ENDPOINT_SEND_QUEUE_DEPTH="${RDMA_ENDPOINT_SEND_QUEUE_DEPTH:-0}"
export RDMA_MULTIPLEX="${RDMA_MULTIPLEX:-false}"
export RDMA_MULTIPLEX_SEND_QUEUE_DEPTH="${RDMA_MULTIPLEX_SEND_QUEUE_DEPTH:-0}"
export AWS_RDMA_ENDPOINT_MUX_RECV_MODE="${AWS_RDMA_ENDPOINT_MUX_RECV_MODE:-}"
export AWS_RDMA_ENDPOINT_MUX_RECV_UNSAFE="${AWS_RDMA_ENDPOINT_MUX_RECV_UNSAFE:-}"

if [ "$MODES" = "tcp" ] || [ "$MODES" = "rdma" ]; then
  MODES="$MODES" "$ROOT_DIR/scripts/compare_tcp_rdma.sh"
  exit 0
fi

if [ "$MODES" != "both" ]; then
  echo "MODES must be one of: both, tcp, rdma" >&2
  exit 1
fi

MODES=tcp "$ROOT_DIR/scripts/compare_tcp_rdma.sh"

if [ "$RESTART_REMOTE_BETWEEN_MODES" = "true" ]; then
  echo
  echo "restarting remote server between tcp and rdma runs"
  "$ROOT_DIR/scripts/stop_remote_server.sh" || true
  "$ROOT_DIR/scripts/deploy_remote_server.sh"
fi

MODES=rdma "$ROOT_DIR/scripts/compare_tcp_rdma.sh"
