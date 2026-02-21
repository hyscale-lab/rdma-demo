# server-client-demo

A runnable skeleton for validating the `s3-rdma` branch in `aws-sdk-go-v2` and comparing RDMA vs TCP behavior.

## Layout

- `cmd/inmem-s3-server`: In-memory S3-compatible server
- `cmd/bench-client`: Benchmark client based on AWS SDK v2 S3 client
- `scripts/compare_tcp_rdma.sh`: Local/endpoint comparison helper
- `scripts/compare_cross_host.sh`: Cross-host comparison helper (`10.0.1.1 -> 10.0.1.2`)
- `scripts/deploy_remote_server.sh`: Build, sync, and start remote server
- `scripts/stop_remote_server.sh`: Stop remote server

## Requirements

- Go `1.23.0+`
- RDMA mode requires:
  - Linux
  - `CGO_ENABLED=1`
  - `-tags rdma`
  - libraries: `librdmacm`, `libibverbs`

## Server Flags (important)

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
  --tcp-listen 127.0.0.1:9000
```

3. Run TCP benchmark

```bash
go run ./cmd/bench-client \
  --mode tcp \
  --endpoint http://127.0.0.1:9000 \
  --op put-get \
  --iterations 10000 \
  --concurrency 64 \
  --object-size 4096
```

4. Start server with RDMA listener

```bash
go run -tags rdma ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:9000 \
  --enable-rdma \
  --rdma-listen 127.0.0.1:19090
```

5. Run RDMA benchmark

```bash
go run -tags rdma ./cmd/bench-client \
  --mode rdma \
  --endpoint http://127.0.0.1:19090 \
  --allow-fallback=false \
  --op put-get \
  --iterations 10000 \
  --concurrency 64 \
  --object-size 4096
```

## Capacity-Limited Experiments

### A) 1GiB hard limit, reject when full

Server:

```bash
go run -tags rdma ./cmd/inmem-s3-server \
  --tcp-listen 10.0.1.2:10090 \
  --enable-rdma \
  --rdma-listen 10.0.1.2:19090 \
  --store-max-bytes 1073741824 \
  --store-evict-policy reject
```

Client (example):

```bash
go run -tags rdma ./cmd/bench-client \
  --mode rdma \
  --endpoint http://10.0.1.2:19090 \
  --allow-fallback=false \
  --op put \
  --iterations 500000 \
  --concurrency 64 \
  --object-size 4096
```

Expected: once full, requests start failing with `InsufficientStorage`.

### B) 1GiB rolling window, FIFO eviction

Server:

```bash
go run -tags rdma ./cmd/inmem-s3-server \
  --tcp-listen 10.0.1.2:10090 \
  --enable-rdma \
  --rdma-listen 10.0.1.2:19090 \
  --store-max-bytes 1073741824 \
  --store-evict-policy fifo
```

Client (example):

```bash
go run -tags rdma ./cmd/bench-client \
  --mode rdma \
  --endpoint http://10.0.1.2:19090 \
  --allow-fallback=false \
  --op put-get \
  --iterations 500000 \
  --concurrency 64 \
  --object-size 4096
```

Expected: store remains bounded near 1GiB while old objects are evicted.

## Scripted Cross-Host Flow

`compare_cross_host.sh` now prepares the remote server by default on every run:

1. stop any stale in-memory server process on `10.0.1.2`
2. deploy and start a fresh server
3. run TCP and RDMA benchmark client from current host

Run a clean cross-host comparison:

```bash
./scripts/compare_cross_host.sh
```

If you already prepared the server manually and want to skip auto reset/deploy:

```bash
PREPARE_REMOTE_SERVER=false ./scripts/compare_cross_host.sh
```

Manual controls are still available:

```bash
./scripts/deploy_remote_server.sh
./scripts/stop_remote_server.sh
```

You can override remote startup capacity via env vars:

```bash
STORE_MAX_BYTES=2147483648 STORE_EVICT_POLICY=fifo ./scripts/compare_cross_host.sh
```
