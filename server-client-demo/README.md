# server-client-demo

## Requirements

- Go `1.23.0+`
- RDMA mode requires:
  - Linux
  - `CGO_ENABLED=1`
  - `-tags rdma`
  - libraries: `librdmacm`, `libibverbs`

## Server Flags

- `--max-object-size`: per-object payload limit (default `64MiB`, `<=0` means unlimited)
- `--store-max-bytes`: total in-memory store capacity (`<=0` means unlimited)
- `--store-evict-policy`: behavior when store is full:
  - `reject`: return `507 InsufficientStorage`
  - `fifo`: evict oldest objects until there is enough space

## Quick Start

1. Resolve deps

```bash
cd server-client-demo
go mod tidy
```

2. Start server (TCP only)

```bash
go run ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:10090
```

3. Start server with RDMA listener

```bash
go run -tags rdma ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:10090 \
  --enable-rdma \
  --rdma-listen 127.0.0.1:10190
```

## Some Script
Can simply use `run_cross_host_*.sh`

- `RDMA_FRAME_PAYLOAD`: change the rdma frame size
- `RDMA_SEND_DEPTH`, `RDMA_RECV_DEPTH`: change the Queue Depth, increase this setting when the payload is large.
- `STORE_MAX_BYTES`: Server storage resident memory size

for example

```
 MODES=both PREPARE_REMOTE_SERVER=true RESTART_REMOTE_BETWEEN_MODES=false S3_CLIENT_COUNT=140 OPEN_LOOP_CLIENT_FANOUT=true TARGET_RPS=3000 DURATION=20s OBJECT_SIZE=262144 WARMUP=280 REQUEST_TIMEOUT=5s RDMA_SHARED_HTTP_POOL=false RDMA_MAX_CONNS_PER_HOST=0 RDMA_ENDPOINT_POOL_SIZE=0 RDMA_ENDPOINT_MULTIPLEX=false RDMA_MULTIPLEX=false /users/Liquidz/rdma-demo/server-client-demo/scripts/compare_cross_host.sh
```