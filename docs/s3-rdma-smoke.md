# `s3-rdma-smoke`

`s3-rdma-smoke` is the combined smoke tool for the standalone `s3-rdma-server`.

Transport mapping:

- TCP mode uses the standard `service/s3` client.
- RDMA mode uses `s3rdmaclient`.
- There is no supported RDMA mode through the standard `service/s3` client.

## TCP GET

```bash
./s3-rdma-smoke \
  --transport tcp \
  --endpoint http://127.0.0.1:10090 \
  --bucket smoke-bucket \
  --key preloaded.txt \
  --op get \
  --payload-size 7
```

## TCP PUT-GET

Use a fresh key. The expected result is:

- `PUT` succeeds
- the follow-up `GET` reports the key missing because the server discards uploaded objects

```bash
./s3-rdma-smoke \
  --transport tcp \
  --endpoint http://127.0.0.1:10090 \
  --bucket smoke-bucket \
  --key tcp-putget.bin \
  --op put-get \
  --payload-size 1024
```

## RDMA GET

For live RDMA runs, point `--endpoint` at an RDMA-backed interface address, not `127.0.0.1`.

```bash
./s3-rdma-smoke \
  --transport rdma \
  --endpoint 10.0.1.1:10191 \
  --bucket smoke-bucket \
  --key preloaded.txt \
  --op get \
  --payload-size 7
```

## RDMA PUT-GET

```bash
./s3-rdma-smoke \
  --transport rdma \
  --endpoint 10.0.1.1:10191 \
  --bucket smoke-bucket \
  --key rdma-putget.bin \
  --rdma-shared-memory-path /dev/shm/khala/smoke-verify.img \
  --op put-get \
  --payload-size 1024
```

## RDMA File-Backed Shared Memory

When `--rdma-shared-memory-path` is set, the smoke tool creates or reuses that file, truncates it to `--rdma-shared-memory-size`, and mmaps it with `MAP_SHARED`.
When the flag is empty, the smoke tool keeps the previous anonymous-mmap behavior.

Recommended routine verification path:

```bash
./s3-rdma-smoke \
  --transport rdma \
  --endpoint 10.0.1.1:10191 \
  --bucket smoke-bucket \
  --key rdma-putget.bin \
  --rdma-shared-memory-path /dev/shm/khala/smoke-verify.img \
  --op put-get \
  --payload-size 1024
```

## RDMA Build Note

For live RDMA smoke runs, build the binaries with RDMA enabled:

```bash
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-server
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-smoke
```
