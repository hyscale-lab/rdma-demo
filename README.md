# server-client-demo

This repo contains an S3-compatible in-memory object server used for network bandwidth and transport benchmarking over:

- regular HTTP/TCP
- the custom RDMA zcopy protocol used by [`cmd/s3-rdma-zcopy-demo`](/users/nehalem/rdma-demo/cmd/s3-rdma-zcopy-demo/main.go)

## Current Object Semantics

- `GET` / `HEAD` / bucket listing read from the server's in-memory payload store.
- The payload store is populated from a folder tree:
  top-level directories are bucket names and nested files are object keys.
- `PUT` uploads are accepted, size-validated, hashed for their response `ETag`, and then discarded.
- A `PUT` object is not expected to be readable later unless that key already exists in the preloaded payload tree.
- If a key already exists in the payload store, later `PUT` requests do not overwrite it.

Example mapping:

- payload tree path:
  `payload-root/nexus-benchmark-payload/input_payload/mapper/part-00000.csv`
- served as:
  `s3://nexus-benchmark-payload/input_payload/mapper/part-00000.csv`

## Requirements

- Go `1.23.0+`
- RDMA mode requires:
  - Linux
  - `CGO_ENABLED=1`
  - `-tags rdma`
  - libraries: `librdmacm`, `libibverbs`

## Config Reference

For a complete description of every server flag, its default, and recommended launch profiles, see [docs/inmem-s3-server-config.md](/users/nehalem/rdma-demo/docs/inmem-s3-server-config.md).

## Important Flags

- `--payload-root`: preload a local payload tree into the in-memory GET store at startup
- `--control-grpc-listen`: expose the gRPC control plane for runtime folder loading and cleanup
- `--max-object-size`: per-upload payload limit
- `--store-max-bytes`: maximum resident bytes for the in-memory payload store
- `--store-evict-policy`: store-full behavior, either `reject` or `fifo`

## Quick Start

1. Resolve deps

```bash
go mod tidy
```

2. Prepare a payload tree

```bash
python3 scripts/prepare_tcp_bucket_object.py \
  --root ./payload-root \
  --bucket nexus-benchmark-payload \
  --key input_payload/mapper/part-00000.csv \
  --size 1048576 \
  --overwrite
```

3. Start the server with the payload tree preloaded

```bash
go run ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:10090 \
  --payload-root ./payload-root \
  --control-grpc-listen 127.0.0.1:19090
```

You can also inspect the grouped CLI help directly:

```bash
go run ./cmd/inmem-s3-server --help
```

4. Run the TCP smoke test

```bash
python3 scripts/boto3_tcp_smoke.py \
  --endpoint http://127.0.0.1:10090 \
  --verify-preloaded-bucket nexus-benchmark-payload \
  --verify-preloaded-key input_payload/mapper/part-00000.csv \
  --verify-preloaded-size 1048576 \
  --verify-preloaded-mode pattern
```

5. Start the RDMA zcopy listener too

```bash
CGO_ENABLED=1 go run -tags rdma ./cmd/inmem-s3-server \
  --tcp-listen 127.0.0.1:10090 \
  --enable-rdma-zcopy \
  --rdma-zcopy-listen 127.0.0.1:10191 \
  --payload-root ./payload-root \
  --control-grpc-listen 127.0.0.1:19090
```

6. Run the zcopy demo against a preloaded key

```bash
CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-zcopy-demo \
  --endpoint 127.0.0.1:10191 \
  --bucket nexus-benchmark-payload \
  --key input_payload/mapper/part-00000.csv \
  --concurrent-get 8
```

## gRPC Control Plane

The control plane is defined in [`proto/inmems3/control/v1/control.proto`](/users/nehalem/rdma-demo/proto/inmems3/control/v1/control.proto) and currently supports:

- `AddFolder`
- `ListBuckets`
- `DeleteBucket`
- `ClearAll`

If you have `grpcurl` installed, a typical runtime load looks like this:

```bash
grpcurl -plaintext \
  -import-path . \
  -proto proto/inmems3/control/v1/control.proto \
  -d '{"path":"./payload-root"}' \
  127.0.0.1:19090 \
  inmems3.control.v1.ControlService/AddFolder
```

List buckets:

```bash
grpcurl -plaintext \
  -import-path . \
  -proto proto/inmems3/control/v1/control.proto \
  -d '{}' \
  127.0.0.1:19090 \
  inmems3.control.v1.ControlService/ListBuckets
```

Clear all loaded buckets:

```bash
grpcurl -plaintext \
  -import-path . \
  -proto proto/inmems3/control/v1/control.proto \
  -d '{}' \
  127.0.0.1:19090 \
  inmems3.control.v1.ControlService/ClearAll
```

## Preparing Benchmark Payload Trees

For `bench-client --op get`, the server must already contain matching readable keys. The helper script can generate the exact key pattern that the benchmark client uses:

```bash
python3 scripts/prepare_tcp_bucket_object.py \
  --root ./payload-root \
  --bucket bench-bucket \
  --key-prefix tcp-sdk-demo \
  --count 1000 \
  --size $((256*1024)) \
  --overwrite
```

This generates keys like:

- `tcp-sdk-demo-00000000`
- `tcp-sdk-demo-00000001`
- `tcp-sdk-demo-00000002`

That matches [`cmd/bench-client/main.go`](/users/nehalem/rdma-demo/cmd/bench-client/main.go) exactly.

## Scripts

You can still use the `run_cross_host_*.sh` helpers. The important changes are:

- `scripts/deploy_remote_server.sh` now accepts:
  `PAYLOAD_ROOT`
  `CONTROL_GRPC_LISTEN`
- `scripts/run_cross_host_tcp_sdk_demo.sh` now defaults to `OP=put`
- `scripts/run_cross_host_tcp_sdk_demo.sh OP=get` requires preloaded payload objects, typically via `PAYLOAD_ROOT`

Example remote deploy with preload plus control plane:

```bash
export REMOTE="user@10.0.1.2"
export REMOTE_DIR="/users/user/rdma-demo/server-client-demo"
export TCP_LISTEN="10.0.1.2:10090"
export RDMA_ZCOPY_LISTEN="10.0.1.2:10191"
export CONTROL_GRPC_LISTEN="10.0.1.2:19090"
export PAYLOAD_ROOT="$REMOTE_DIR/payload-root"

./scripts/stop_remote_server.sh
ENABLE_RDMA_ZCOPY=true \
REMOTE="$REMOTE" \
REMOTE_DIR="$REMOTE_DIR" \
TCP_LISTEN="$TCP_LISTEN" \
RDMA_ZCOPY_LISTEN="$RDMA_ZCOPY_LISTEN" \
CONTROL_GRPC_LISTEN="$CONTROL_GRPC_LISTEN" \
PAYLOAD_ROOT="$PAYLOAD_ROOT" \
./scripts/deploy_remote_server.sh
```

## Verification Commands

Default build:

```bash
go test ./...
```

RDMA-tagged build:

```bash
CGO_ENABLED=1 go test -tags rdma ./...
```
