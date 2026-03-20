# `s3-rdma-server` Project Spec

## Summary

`s3-rdma-server` is a new, standalone benchmark server that mimics a remote object store while staying intentionally small.

It must:

- speak the minimal S3-compatible HTTP/TCP API already used in this repo
- speak the RDMA zcopy protocol expected by the modified `aws-sdk-go-v2`
- discard all `PUT` payloads after validating and acknowledging receipt
- keep only startup-loaded `GET` objects in memory
- run as a normal CLI binary suitable for tmux and external orchestration
- avoid any code-level dependency on `inmem-s3-server`

This server is for network transport research, not storage research. Simplicity, predictability, and low maintenance matter more than feature completeness.

## Goals

- Provide a compact standalone binary at `cmd/s3-rdma-server`.
- Preserve the current minimal benchmark semantics for TCP.
- Add RDMA zcopy support compatible with `s3rdmaclient`.
- Keep runtime state limited to:
  bucket/object metadata for startup-loaded objects
  startup-loaded object bytes for `GET`
  transient request/transport state
- Make the binary easy to launch directly or inside tmux with a clear CLI.
- Provide a compact smoke tool for TCP and RDMA validation.

## Non-Goals

- No runtime control API, gRPC API, or reload endpoint.
- No non-zcopy RDMA mode.
- No retention of uploaded `PUT` objects.
- No store eviction policy, memory quota policy, or capacity manager in v1.
- No auth, IAM, ACL, multipart upload, versioning, tagging, lifecycle rules, range requests, or other full-S3 features.
- No dependency on `internal/inmems3/*`.

## Core Semantics

### Object Lifetime

- Startup-loaded objects from `--payload-root` are the only persistent readable objects.
- `PUT` receives the payload, enforces size limits, returns success metadata, and discards the body.
- A key uploaded via `PUT` is not expected to be readable later unless the same key already existed in the startup-loaded payload set.

### Payload Source

- `--payload-root` is optional.
- If provided:
  top-level directories are bucket names
  nested files become object keys
- If omitted:
  the server starts with no readable objects, but bucket creation and upload receipt still work.

### Bucket Behavior

- Buckets may be created by HTTP bucket PUT, RDMA `EnsureBucketReq`, or implicitly during object PUT.
- Buckets exist even if they contain no readable objects.
- Deleting a bucket removes all startup-loaded readable objects under that bucket from in-memory state.

## Public Runtime Surface

### Binary

- `cmd/s3-rdma-server`
- standalone foreground process
- clean exit on `SIGINT` and `SIGTERM`
- no built-in daemon or tmux logic

### CLI Config Surface

Required v1 flags:

- `--debug`
- `--tcp-listen` default `127.0.0.1:10090`
- `--enable-rdma-zcopy`
- `--rdma-zcopy-listen` default `127.0.0.1:10191`
- `--region` default `us-east-1`
- `--payload-root`
- `--max-object-size` default `67108864`
- `--rdma-backlog`
- `--rdma-accept-workers`
- `--rdma-lowcpu`
- `--rdma-frame-payload`
- `--rdma-send-depth`
- `--rdma-recv-depth`
- `--rdma-inline-threshold`
- `--rdma-send-signal-interval`

Flag rules:

- at least one data listener must be enabled
- TCP is enabled when `--tcp-listen` is non-empty
- RDMA zcopy is enabled only when `--enable-rdma-zcopy` is set
- `--payload-root` only affects startup preload
- RDMA tuning flags are passed through to the RDMA transport layer

Removed from the new design:

- control-plane listener flags
- runtime folder-load flags
- store capacity and eviction flags

## Operation Matrix

| Path | Operation | Required Behavior |
| --- | --- | --- |
| HTTP/TCP | `GET /` | list buckets |
| HTTP/TCP | `PUT /bucket` | create bucket |
| HTTP/TCP | `HEAD /bucket` | check bucket existence |
| HTTP/TCP | `GET /bucket?prefix=&max-keys=` | list objects |
| HTTP/TCP | `DELETE /bucket` | delete bucket and its readable objects |
| HTTP/TCP | `PUT /bucket/key` | receive, size-check, hash, discard, return success |
| HTTP/TCP | `GET /bucket/key` | return startup-loaded object bytes if present |
| HTTP/TCP | `HEAD /bucket/key` | return metadata for startup-loaded object |
| HTTP/TCP | `DELETE /bucket/key` | delete readable object entry if present |
| RDMA zcopy | `HelloReq` / `HelloResp` | handshake and credit setup |
| RDMA zcopy | `Ack` | credit return |
| RDMA zcopy | `EnsureBucketReq` | create bucket |
| RDMA zcopy | `PutReq` | validate, receive payload, discard, return `RespOK` |
| RDMA zcopy | `GetReq` | fetch startup-loaded object, send `GetMeta` + payload |
| RDMA zcopy | `RespOK` / `RespErr` | success and error responses |

## Architecture Constraints

- New code must live under:
  `cmd/s3-rdma-server`
  `internal/s3rdmaserver`
  `pkg/s3rdmasmoke`
  `cmd/s3-rdma-smoke`
- The implementation must not import `internal/inmems3/*`.
- The implementation must not reuse `inmem-s3-server` names in package, binary, or docs.
- The TCP code should preserve current client-visible behavior where feasible.
- The RDMA service should preserve current `s3rdmaclient` compatibility where feasible.

## Step-By-Step Implementation Contract

### Step 1. Spec And Design Artifacts

- Create this spec file.
- Create `docs/s3-rdma-server-architecture.md`.
- Stop for explicit approval.

### Step 2. Project Skeleton And CLI Contract

- Add `internal/s3rdmaserver/{app,store,s3api,zcopy}`.
- Add `pkg/s3rdmasmoke`.
- Wire `cmd/s3-rdma-server/main.go` to flags, config validation, and app startup.
- Stop for explicit approval.

### Step 3. Simple Store And Startup Loader

- Implement the minimal in-memory GET store.
- Implement startup `--payload-root` loading.
- Add unit tests for store and loader behavior.
- Stop for explicit approval.

### Step 4. Minimal S3/TCP Handler

- Implement the HTTP bucket/object surface.
- Keep `PUT` as receive-and-discard.
- Add handler tests.
- Stop for explicit approval.

### Step 5. RDMA Zcopy Service

- Implement only the protocol needed by the current SDK.
- Keep `PUT` as receive-and-discard.
- Add zcopy tests and RDMA verification.
- Stop for explicit approval.

### Step 6. Standalone Lifecycle And CLI

- Finish listener startup, preload, shutdown, and operator-oriented help output.
- Verify local launch behavior.
- Stop for explicit approval.

### Step 7. Combined Smoke Tool

- Build `cmd/s3-rdma-smoke`.
- Support both TCP and RDMA modes.
- Verify with local smoke runs.
- Stop for explicit approval.

### Step 8. Final Verification And Cleanup

- Update repo docs for the new server and tool.
- Run final test/build/manual verification.
- Stop for explicit approval.

## Acceptance Criteria

- `s3-rdma-server` builds and runs without importing `internal/inmems3/*`.
- TCP behavior remains compatible with the existing benchmark clients for the supported API subset.
- RDMA zcopy behavior works with the modified SDK client in this repository.
- `PUT` payloads are never retained for later reads.
- `GET`/`HEAD`/listing use only startup-loaded data.
- The smoke tool can validate TCP and RDMA flows.
- The resulting codebase is materially smaller and easier to move into a separate repo than the current `inmem-s3-server`.

## Verification Plan

- Unit tests for store, loader, HTTP handler, and zcopy service.
- `go build ./cmd/s3-rdma-server`
- `go build ./cmd/s3-rdma-smoke`
- `go test ./...`
- `CGO_ENABLED=1 go test -tags rdma ./...`
- Manual TCP smoke run.
- Manual RDMA smoke run.
