# server-client-demo

This repo now contains a new standalone benchmark server, `s3-rdma-server`, plus a combined validation tool, `s3-rdma-smoke`.

The new server is intentionally small:

- TCP uses a minimal S3-compatible HTTP path.
- RDMA uses only the zero-copy protocol expected by the modified local `aws-sdk-go-v2`.
- `PUT` uploads are accepted and discarded.
- Only startup-loaded payload-root objects are readable through `GET` / `HEAD` / list.

The supported RDMA client path in this repo is
`aws/transport/http/rdma/s3rdmaclient`. The standard `service/s3` client is
used only for TCP/HTTP here.

## Main Entrypoints

- Server: [cmd/s3-rdma-server/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go)
- Smoke tool: [cmd/s3-rdma-smoke/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-smoke/main.go)

## Repo Layout

- [cmd/s3-rdma-server](/users/nehalem/rdma-demo/cmd/s3-rdma-server): standalone server binary
- [cmd/s3-rdma-smoke](/users/nehalem/rdma-demo/cmd/s3-rdma-smoke): combined TCP + RDMA smoke binary
- [internal/s3rdmaserver](/users/nehalem/rdma-demo/internal/s3rdmaserver): isolated server implementation
- [pkg/s3rdmasmoke](/users/nehalem/rdma-demo/pkg/s3rdmasmoke): shared smoke-tool logic
- [docs/s3-rdma-server-architecture.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-architecture.md): design diagrams
- [docs/s3-rdma-server-config.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-config.md): flag reference
- [docs/s3-rdma-server-operations.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-operations.md): launch patterns
- [docs/s3-rdma-smoke.md](/users/nehalem/rdma-demo/docs/s3-rdma-smoke.md): smoke examples

## Object Semantics

- `GET`, `HEAD`, and object listing read only from the startup-loaded payload tree.
- Top-level directories under `--payload-root` become buckets.
- Nested files under each bucket directory become object keys.
- `PUT` receives the body, validates size, returns success metadata, and discards the object body.
- A `PUT` key is not expected to be readable later unless that key was already preloaded at startup.

Example mapping:

- `payload-root/nexus-benchmark-payload/input_payload/mapper/part-00000.csv`
- `s3://nexus-benchmark-payload/input_payload/mapper/part-00000.csv`

## Build Requirements

- Go `1.26.1+`
- TCP-only builds:
  `go build ./cmd/s3-rdma-server ./cmd/s3-rdma-smoke`
- Live RDMA builds:
  `CGO_ENABLED=1`
  `-tags rdma`
  Linux with RDMA userspace libraries available

## Quick Start

1. Create a payload tree.

```bash
mkdir -p ./payload-root/smoke-bucket
printf 'payload' > ./payload-root/smoke-bucket/preloaded.txt
```

2. Start the standalone server over TCP.

```bash
go run ./cmd/s3-rdma-server \
  --tcp-listen 127.0.0.1:10090 \
  --payload-root ./payload-root
```

3. Validate the preloaded GET path.

```bash
go run ./cmd/s3-rdma-smoke \
  --transport tcp \
  --endpoint http://127.0.0.1:10090 \
  --bucket smoke-bucket \
  --key preloaded.txt \
  --op get \
  --payload-size 7
```

4. Validate the discard-on-PUT behavior.

```bash
go run ./cmd/s3-rdma-smoke \
  --transport tcp \
  --endpoint http://127.0.0.1:10090 \
  --bucket smoke-bucket \
  --key tcp-putget.bin \
  --op put-get \
  --payload-size 1024
```

5. Build RDMA-enabled binaries for live RDMA validation.

```bash
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-server
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-smoke
```

6. Start the server with RDMA enabled on an RDMA-backed interface address.

```bash
./s3-rdma-server \
  --tcp-listen 127.0.0.1:10090 \
  --enable-rdma-zcopy \
  --rdma-zcopy-listen 10.0.1.1:10191 \
  --payload-root ./payload-root
```

7. Run the RDMA smoke tool against that same RDMA-backed interface address.

```bash
./s3-rdma-smoke \
  --transport rdma \
  --endpoint 10.0.1.1:10191 \
  --bucket smoke-bucket \
  --key preloaded.txt \
  --op get \
  --payload-size 7
```

For live RDMA runs, do not use `127.0.0.1` unless your environment specifically supports loopback RDMA routing. In typical setups, use the IP address of the active RDMA netdev.

## Help Output

```bash
go run ./cmd/s3-rdma-server --help
go run ./cmd/s3-rdma-smoke --help
```

## Verification Commands

```bash
go test ./...
CGO_ENABLED=1 go test -tags rdma ./...
go build ./cmd/...
```
