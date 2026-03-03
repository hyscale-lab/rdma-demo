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

3. Start server with RDMA zcopy protocol listener

```bash
go run -tags rdma ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:10090 \
  --enable-rdma-zcopy \
  --rdma-zcopy-listen 127.0.0.1:10191
```

4. Run zcopy demo (returns `offset/size` in shared memory)

```bash
CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-zcopy-demo \
  --endpoint 127.0.0.1:10191 \
  --bucket bench-bucket \
  --key zcopy-demo \
  --concurrent-get 8
```

## Some Script
Can simply use `run_cross_host_*.sh`

- `RDMA_FRAME_PAYLOAD`: change the rdma frame size
- `RDMA_SEND_DEPTH`, `RDMA_RECV_DEPTH`: change the Queue Depth, increase this setting when the payload is large.
- `STORE_MAX_BYTES`: Server storage resident memory size

for example

```
 CLIENT_COUNT=140 TARGET_RPS=500 DURATION=20s MEM_SIZE=$((32*1024*1024)) CONCURRENT_GET=8 /users/Liquidz/rdma-demo/server-client-demo/scripts/run_cross_host_rdma_zcopy_demo.sh
```

for zcopy protocol smoke test

```bash
/users/Liquidz/rdma-demo/server-client-demo/scripts/run_cross_host_rdma_zcopy_demo.sh
```

for zcopy rps sweep (500..3000, step 500)

```bash
CLIENT_COUNT=140 MEM_SIZE=$((32*1024*1024)) DURATION=20s \
/users/Liquidz/rdma-demo/server-client-demo/scripts/run_cross_host_rdma_zcopy_rps_sweep.sh
```
