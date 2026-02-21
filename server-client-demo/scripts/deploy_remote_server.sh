#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REMOTE="${REMOTE:-Liquidz@10.0.1.2}"
REMOTE_DIR="${REMOTE_DIR:-/users/Liquidz/rdma-demo/server-client-demo}"
TCP_LISTEN="${TCP_LISTEN:-10.0.1.2:10090}"
RDMA_LISTEN="${RDMA_LISTEN:-10.0.1.2:19090}"
ENABLE_RDMA="${ENABLE_RDMA:-true}"
REGION="${REGION:-us-east-1}"
MAX_OBJECT_SIZE="${MAX_OBJECT_SIZE:-67108864}"
STORE_MAX_BYTES="${STORE_MAX_BYTES:-0}"
STORE_EVICT_POLICY="${STORE_EVICT_POLICY:-reject}"

LOCAL_BIN="$ROOT_DIR/bin/inmem-s3-server-rdma"
REMOTE_LOG_DIR="$REMOTE_DIR/logs"
REMOTE_PID_FILE="$REMOTE_LOG_DIR/inmem-s3-server.pid"
REMOTE_LOG_FILE="$REMOTE_LOG_DIR/inmem-s3-server.log"

echo "building local server binary with RDMA tag"
mkdir -p "$ROOT_DIR/bin"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=1 go build -tags rdma -o "$LOCAL_BIN" ./cmd/inmem-s3-server
)

echo "syncing project to $REMOTE:$REMOTE_DIR"
rsync -az --delete "$ROOT_DIR/" "$REMOTE:$REMOTE_DIR/"

echo "starting remote server"
ssh "$REMOTE" "set -euo pipefail
mkdir -p '$REMOTE_LOG_DIR'
cd '$REMOTE_DIR'
if [ -f '$REMOTE_PID_FILE' ]; then
  OLD_PID=\$(cat '$REMOTE_PID_FILE' || true)
  if [ -n \"\$OLD_PID\" ] && kill -0 \"\$OLD_PID\" 2>/dev/null; then
    kill \"\$OLD_PID\" || true
    sleep 1
  fi
fi
pkill -x inmem-s3-server-rdma >/dev/null 2>&1 || true
sleep 1
CMD=(\"./bin/inmem-s3-server-rdma\" \"--tcp-listen\" \"$TCP_LISTEN\" \"--region\" \"$REGION\" \"--max-object-size\" \"$MAX_OBJECT_SIZE\" \"--store-max-bytes\" \"$STORE_MAX_BYTES\" \"--store-evict-policy\" \"$STORE_EVICT_POLICY\")
if [ \"$ENABLE_RDMA\" = \"true\" ]; then
  CMD+=(\"--enable-rdma\" \"--rdma-listen\" \"$RDMA_LISTEN\")
fi
nohup \"\${CMD[@]}\" > '$REMOTE_LOG_FILE' 2>&1 &
echo \$! > '$REMOTE_PID_FILE'
sleep 1
PID=\$(cat '$REMOTE_PID_FILE')
if ! kill -0 \"\$PID\" 2>/dev/null; then
  echo \"remote server failed to start\" >&2
  tail -n 200 '$REMOTE_LOG_FILE' >&2 || true
  exit 1
fi
echo pid=\$PID
ps -p \"\$PID\" -o pid,cmd --no-headers || true
tail -n 20 '$REMOTE_LOG_FILE'
"

echo
echo "remote server started"
echo "tcp endpoint:  http://$TCP_LISTEN"
if [ "$ENABLE_RDMA" = "true" ]; then
  echo "rdma endpoint: http://$RDMA_LISTEN"
fi
