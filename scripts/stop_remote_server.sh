#!/usr/bin/env bash
set -euo pipefail

REMOTE="${REMOTE:-Liquidz@10.0.1.2}"
REMOTE_DIR="${REMOTE_DIR:-/users/Liquidz/rdma-demo/server-client-demo}"
PID_FILE="$REMOTE_DIR/logs/inmem-s3-server.pid"

ssh "$REMOTE" "set -euo pipefail
stopped='false'
if [ -f '$PID_FILE' ]; then
  PID=\$(cat '$PID_FILE' || true)
  if [ -n \"\$PID\" ] && kill -0 \"\$PID\" 2>/dev/null; then
    kill \"\$PID\" || true
    sleep 1
    if kill -0 \"\$PID\" 2>/dev/null; then
      kill -9 \"\$PID\" || true
    fi
    echo \"stopped pid=\$PID from pid file\"
    stopped='true'
  fi
fi

pkill -x inmem-s3-server-rdma >/dev/null 2>&1 || true
pkill -x inmem-s3-server >/dev/null 2>&1 || true
sleep 1
pkill -9 -x inmem-s3-server-rdma >/dev/null 2>&1 || true
pkill -9 -x inmem-s3-server >/dev/null 2>&1 || true

rm -f '$PID_FILE'

if [ \"\$stopped\" = 'false' ]; then
  echo 'remote server cleanup done (no active pid in pid file)'
fi
"
