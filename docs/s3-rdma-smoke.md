# `s3-rdma-smoke`

`s3-rdma-smoke` is the combined smoke tool for the standalone `s3-rdma-server`.

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
  --op put-get \
  --payload-size 1024
```

## RDMA Build Note

For live RDMA smoke runs, build the binaries with RDMA enabled:

```bash
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-server
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-smoke
```
