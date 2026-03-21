# `s3-rdma-server` Config Reference

## General

- `--debug`
  Enables debug logging.
- `--region`
  Region string returned by the S3-compatible API.
  Default: `us-east-1`
- `--payload-root`
  Optional startup preload directory.
  Top-level folders become buckets and nested files become object keys.
- `--max-object-size`
  Maximum accepted upload size in bytes.
  Values `<= 0` disable the limit.
  Default: `67108864`

## Listeners

- `--tcp-listen`
  HTTP/TCP listener address.
  Empty disables TCP.
  Default: `127.0.0.1:10090`
- `--enable-rdma-zcopy`
  Enables the RDMA zero-copy listener.
  Default: `false`
- `--rdma-zcopy-listen`
  RDMA zero-copy listener address.
  Default: `127.0.0.1:10191`
  For real RDMA traffic, set this to an active RDMA-backed interface address instead of loopback.

RDMA client note:

- The supported RDMA client path is `s3rdmaclient`.
- The standard `service/s3` client in this repo is TCP/HTTP-only.

## RDMA Tuning

- `--rdma-backlog`
  RDMA listener backlog.
  Default: `512`
  `0` uses the RDMA library default.
- `--rdma-accept-workers`
  RDMA accept worker count.
  Default: `1`
  `<= 0` uses the RDMA library default.
- `--rdma-frame-payload`
  RDMA frame payload size in bytes.
  Default: `65536`
- `--rdma-send-depth`
  RDMA send queue depth.
  Default: `64`
- `--rdma-recv-depth`
  RDMA recv queue depth.
  Default: `64`
- `--rdma-inline-threshold`
  RDMA inline threshold in bytes.
  Default: `0`
- `--rdma-lowcpu`
  Prefers lower CPU signaling behavior when the transport is configured for it.
  Default: `false`
- `--rdma-send-signal-interval`
  RDMA send completion interval.
  Default: `1`

## Practical Profiles

### TCP-only

```bash
./s3-rdma-server \
  --tcp-listen 127.0.0.1:10090 \
  --payload-root /path/to/payload-root
```

### TCP + RDMA

```bash
./s3-rdma-server \
  --tcp-listen 127.0.0.1:10090 \
  --enable-rdma-zcopy \
  --rdma-zcopy-listen 10.0.1.1:10191 \
  --payload-root /path/to/payload-root
```

### tmux

```bash
tmux new-session -d -s s3-rdma-server \
  'cd /path/to/repo && ./s3-rdma-server --tcp-listen 127.0.0.1:10090 --payload-root /path/to/payload-root'
```
