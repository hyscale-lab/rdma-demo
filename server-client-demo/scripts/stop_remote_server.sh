#!/usr/bin/env bash
set -euo pipefail

REMOTE="${REMOTE:-Liquidz@10.0.1.2}"
REMOTE_DIR="${REMOTE_DIR:-/users/Liquidz/rdma-demo/server-client-demo}"
PID_FILE="$REMOTE_DIR/logs/inmem-s3-server.pid"

ssh "$REMOTE" "set -euo pipefail
if [ ! -f '$PID_FILE' ]; then
  echo 'pid file not found, nothing to stop'
  exit 0
fi
PID=\$(cat '$PID_FILE' || true)
if [ -z \"\$PID\" ]; then
  echo 'pid file empty, nothing to stop'
  exit 0
fi
if kill -0 \"\$PID\" 2>/dev/null; then
  kill \"\$PID\" || true
  sleep 1
  if kill -0 \"\$PID\" 2>/dev/null; then
    kill -9 \"\$PID\" || true
  fi
  echo \"stopped pid=\$PID\"
else
  echo \"pid \$PID not running\"
fi
"
