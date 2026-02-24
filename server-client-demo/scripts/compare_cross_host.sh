#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REMOTE_HOST="${REMOTE_HOST:-10.0.1.2}"
TCP_PORT="${TCP_PORT:-10090}"
RDMA_PORT="${RDMA_PORT:-10190}"
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

MODES=tcp "$ROOT_DIR/scripts/compare_tcp_rdma.sh"

if [ "$RESTART_REMOTE_BETWEEN_MODES" = "true" ]; then
  echo
  echo "restarting remote server between tcp and rdma runs"
  "$ROOT_DIR/scripts/stop_remote_server.sh" || true
  "$ROOT_DIR/scripts/deploy_remote_server.sh"
fi

MODES=rdma "$ROOT_DIR/scripts/compare_tcp_rdma.sh"
