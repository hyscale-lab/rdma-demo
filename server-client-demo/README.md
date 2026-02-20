# server-client-demo

## Layout

- `cmd/inmem-s3-server`: In-memory S3-compatible server (no persistent storage)
- `cmd/bench-client`: Benchmark client based on AWS SDK v2 S3 client

## Requirements

- Go `1.23.0+`
- RDMA path requires:
  - Linux
  - `CGO_ENABLED=1`
  - `-tags rdma`
  - libraries: `librdmacm`, `libibverbs`

## Quick Start

1. Enter this directory and resolve dependencies

```bash
cd server-client-demo
go mod tidy
```

2. Start server (TCP only)

```bash
go run ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:9000
```

3. Run TCP baseline

```bash
go run ./cmd/bench-client \
  --mode tcp \
  --endpoint http://127.0.0.1:9000 \
  --op put-get \
  --iterations 10000 \
  --concurrency 64 \
  --object-size 4096
```

4. Start RDMA listener

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

## CPU Cycles Comparison

Run:

```bash
./scripts/compare_tcp_rdma.sh
```


default, the script builds the client with `GO_BUILD_FLAGS=-tags rdma` and `RDMA_ALLOW_FALLBACK=false`.
To switch to a non-RDMA build or allow fallback:

```bash
GO_BUILD_FLAGS="" RDMA_ALLOW_FALLBACK=true ./scripts/compare_tcp_rdma.sh
```

## Cross-Host Deployment (10.0.1.1 client -> 10.0.1.2 server)

1. Build locally, sync, and start the remote server

```bash
cd server-client-demo
./scripts/deploy_remote_server.sh
```

By default this starts on `10.0.1.2`:
- TCP: `10.0.1.2:10090`
- RDMA: `10.0.1.2:19090`

override environment variables:

```bash
REMOTE=Liquidz@10.0.1.2 TCP_LISTEN=10.0.1.2:10090 RDMA_LISTEN=10.0.1.2:19090 ./scripts/deploy_remote_server.sh
```

2. Run cross-host TCP/RDMA comparison from local machine

```bash
./scripts/compare_cross_host.sh
```

3. Stop remote server

```bash
./scripts/stop_remote_server.sh
```
