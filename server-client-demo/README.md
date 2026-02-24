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

