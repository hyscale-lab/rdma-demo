# `s3-rdma-server` Operations

This server is intended to run as a standalone binary.

## TCP-only launch

```bash
./s3-rdma-server \
  --tcp-listen 127.0.0.1:10090 \
  --payload-root /path/to/payload-root
```

## TCP + RDMA zcopy launch

Build the server with RDMA enabled first:

```bash
CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-server
```

Then bind the RDMA listener to an active RDMA-backed interface address, not `127.0.0.1`:

```bash
./s3-rdma-server \
  --tcp-listen 127.0.0.1:10090 \
  --enable-rdma-zcopy \
  --rdma-zcopy-listen 10.0.1.1:10191 \
  --payload-root /path/to/payload-root
```

Use `s3rdmaclient`-based tools such as `s3-rdma-smoke --transport rdma` or
`s3-rdma-zcopy-demo` for RDMA validation. The standard `service/s3` client is
TCP-only in this repo.

## tmux launch

```bash
tmux new-session -d -s s3-rdma-server \
  'cd /path/to/repo && ./s3-rdma-server --tcp-listen 127.0.0.1:10090 --payload-root /path/to/payload-root'
```

To stop the server cleanly inside tmux:

```bash
tmux send-keys -t s3-rdma-server C-c
```

The server handles `SIGINT` and `SIGTERM` and shuts down its listeners cleanly.
