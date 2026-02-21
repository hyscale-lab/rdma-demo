#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REMOTE_HOST="${REMOTE_HOST:-10.0.1.2}"
TCP_PORT="${TCP_PORT:-10090}"
RDMA_PORT="${RDMA_PORT:-10190}"
PREPARE_REMOTE_SERVER="${PREPARE_REMOTE_SERVER:-true}"

if [ "$PREPARE_REMOTE_SERVER" = "true" ]; then
  echo "preparing remote server: stop old process and start a fresh instance"
  "$ROOT_DIR/scripts/stop_remote_server.sh" || true
  "$ROOT_DIR/scripts/deploy_remote_server.sh"
fi

export TCP_ENDPOINT="${TCP_ENDPOINT:-http://$REMOTE_HOST:$TCP_PORT}"
export RDMA_ENDPOINT="${RDMA_ENDPOINT:-http://$REMOTE_HOST:$RDMA_PORT}"
export GO_BUILD_FLAGS="${GO_BUILD_FLAGS:--tags rdma}"
export RDMA_ALLOW_FALLBACK="${RDMA_ALLOW_FALLBACK:-false}"

exec "$ROOT_DIR/scripts/compare_tcp_rdma.sh"
