# In-Memory S3 Server Config Reference

This document describes every runtime flag supported by [`cmd/inmem-s3-server`](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main.go).

The server is meant for transport and bandwidth testing, so its object behavior is intentionally asymmetric:

- `GET` / `HEAD` / bucket listing read from the in-memory payload store.
- The payload store is populated from `--payload-root` at startup or through the gRPC control plane.
- `PUT` accepts uploads, validates size, returns success metadata, and then discards the payload bytes.
- `--store-max-bytes` only limits resident GET payloads. It does not retain or cap discarded PUT history beyond the per-request `--max-object-size` limit.

## Listener Rules

- Enable at least one data listener with `--tcp-listen` or `--enable-rdma-zcopy`.
- `--tcp-listen=""` disables the HTTP/S3 listener.
- `--enable-rdma-zcopy=false` disables the RDMA zcopy listener.
- `--control-grpc-listen` is optional and only exposes the control plane. It does not serve S3 traffic by itself.

## Flag Reference

| Flag | Default | What it controls | When to change it |
| --- | --- | --- | --- |
| `--tcp-listen` | `127.0.0.1:10090` | HTTP/S3 listen address. Empty disables TCP. | Set it to the host:port your benchmark clients can reach. |
| `--enable-rdma-zcopy` | `false` | Enables the custom RDMA zcopy listener. | Turn it on only when using the RDMA benchmark/demo clients. |
| `--rdma-zcopy-listen` | `127.0.0.1:10191` | RDMA zcopy listen address. Only used when RDMA is enabled. | Set it to the RDMA-reachable host:port for zcopy clients. |
| `--region` | `us-east-1` | Region string returned by the S3-compatible APIs. | Change it only if a client or test expects a specific region value. |
| `--max-object-size` | `67108864` | Maximum accepted size for a single PUT upload in bytes. Values `<= 0` disable the limit. | Raise it for larger uploads or set `0` for unlimited PUT size. |
| `--payload-root` | empty | Startup preload directory. Top-level folders become buckets and nested files become object keys. | Set it when GET benchmarks need preloaded readable objects at process start. |
| `--control-grpc-listen` | empty | Optional gRPC control-plane listen address for `AddFolder`, `ListBuckets`, `DeleteBucket`, and `ClearAll`. | Enable it when an external controller needs to load or clean payload trees at runtime. |
| `--store-max-bytes` | `0` | Resident memory cap for the in-memory GET payload store. Values `<= 0` mean unlimited. | Use it when preloaded payloads must stay under a bounded memory budget. |
| `--store-evict-policy` | `reject` | Behavior when the payload store is full. Supported values: `reject`, `fifo`. | Use `reject` for predictable failures or `fifo` for rolling replacement of older payloads. |
| `--rdma-backlog` | `512` | RDMA CM listen backlog. `0` keeps the RDMA library default. | Tune it only when RDMA connection setup becomes a bottleneck. |
| `--rdma-accept-workers` | `1` | Concurrent RDMA accept workers. `0` keeps the RDMA library default. | Increase it only after measuring connection-accept pressure. |
| `--rdma-lowcpu` | `false` | Prefers fewer RDMA completion signals when `--rdma-send-signal-interval` is left at `0`. | Enable it when CPU overhead matters more than per-frame completion visibility. |
| `--rdma-frame-payload` | `0` | RDMA frame payload size in bytes. `0` uses the RDMA library default (`65536`). | Tune it only for transport experiments or NIC-specific testing. |
| `--rdma-send-depth` | `0` | RDMA send queue depth. `0` uses the RDMA library default (`64`). | Tune it when outstanding send pressure needs adjustment. |
| `--rdma-recv-depth` | `0` | RDMA receive queue depth. `0` uses the RDMA library default (`64`). | Tune it when receive posting depth needs adjustment. |
| `--rdma-inline-threshold` | `0` | RDMA inline-send threshold in bytes. `0` uses the RDMA library default (`0`). | Tune it only if you are experimenting with inline SEND behavior. |
| `--rdma-send-signal-interval` | `0` | RDMA send completion interval. `0` uses the RDMA library default (`1`) or the low-CPU default (`16`) when `--rdma-lowcpu` is enabled. | Set it explicitly when you want predictable completion signaling independent of `--rdma-lowcpu`. |

## Recommended Launch Profiles

### Local TCP-only development

```bash
./inmem-s3-server \
  --tcp-listen 127.0.0.1:10090
```

### Remote GET benchmark with startup preload

```bash
./inmem-s3-server \
  --tcp-listen 10.0.1.2:10090 \
  --payload-root /srv/inmem-s3/payload-root
```

### Remote benchmark with runtime control plane

```bash
./inmem-s3-server \
  --tcp-listen 10.0.1.2:10090 \
  --payload-root /srv/inmem-s3/payload-root \
  --control-grpc-listen 127.0.0.1:19090
```

Binding the control plane to `127.0.0.1` is a good default when only the local orchestration process needs it.

### Remote TCP + RDMA benchmark server

```bash
./inmem-s3-server \
  --tcp-listen 10.0.1.2:10090 \
  --enable-rdma-zcopy \
  --rdma-zcopy-listen 10.0.1.2:10191 \
  --payload-root /srv/inmem-s3/payload-root \
  --control-grpc-listen 127.0.0.1:19090
```

## tmux-Oriented Launching

If another Go program is responsible for process lifecycle, prefer launching the compiled binary directly and let tmux only own the terminal session:

```bash
tmux new-session -d -s inmem-s3-server \
  "/opt/inmem-s3-server --tcp-listen 10.0.1.2:10090 --enable-rdma-zcopy --rdma-zcopy-listen 10.0.1.2:10191 --payload-root /srv/inmem-s3/payload-root --control-grpc-listen 127.0.0.1:19090"
```

That keeps the deployment surface stable:

- the binary owns all runtime configuration through flags
- tmux only keeps the process attached to a durable session
- your external Go controller can inspect logs, reload payload folders through gRPC, and restart the binary without depending on the old shell script

## Discoverability

Run the binary with `--help` to print the grouped flag summary:

```bash
./inmem-s3-server --help
```
