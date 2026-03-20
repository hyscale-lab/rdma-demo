# In-Memory S3 Server Refactor And Payload Source

## Goal

Refactor the in-memory S3 benchmark server into a maintainable Go repo structure while preserving both access modes:

- S3-compatible HTTP/TCP serving
- RDMA zcopy serving for the custom `s3rdmaclient`

Add a filesystem-backed GET payload source where:

- top-level directories map to bucket names
- nested files map to object keys
- example: `payload-root/nexus-benchmark-payload/input_payload/mapper/part-00000.csv`
  serves `s3://nexus-benchmark-payload/input_payload/mapper/part-00000.csv`

Change PUT semantics so uploaded payloads are received and validated, but not retained for later reads.

## Acceptance Criteria

- HTTP S3 path still supports bucket and object operations needed by the existing clients/scripts.
- RDMA zcopy path still supports `EnsureBucket`, `PutZeroCopy`, and `GetZeroCopy`.
- GET/HEAD for payload-backed objects return bytes and metadata from the configured filesystem payload root.
- PUT accepts object uploads, enforces limits, and discards payload after processing instead of storing it for future GETs.
- Code is reorganized into clear packages under standard Go repo structure.
- Verification exists for the main behavior, not just a build-only check.

## Working Notes

- Current server logic is concentrated in [cmd/inmem-s3-server/main.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main.go) and [cmd/inmem-s3-server/zcopy_service.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/zcopy_service.go).
- `main.go` currently mixes flag parsing, process lifecycle, in-memory storage, S3 XML responses, and HTTP handlers in one file.
- `zcopy_service.go` currently mixes protocol handling, credit flow control, transport concerns, and object lookup in one file.
- Both HTTP GET and RDMA GET currently read from the same in-memory `memoryStore`.
- Both HTTP PUT and RDMA PUT currently retain uploaded bytes in `memoryStore`, which conflicts with the desired benchmark behavior.
- The repo uses a local AWS SDK override via [go.mod](/users/nehalem/rdma-demo/go.mod) and the `aws-sdk-go-v2` submodule.
- The worktree is already dirty because files were moved from `server-client-demo/` into the repo root; avoid modifying unrelated deleted paths.
- `agent/tasks/lessons.md` did not exist yet for this repo, so it is being initialized in this session.
- User requested that future Task 3 include a gRPC control server for managing in-memory storage and payload-folder registration/cleanup.
- Task 2 package layout decision: use `internal/inmems3/app`, `internal/inmems3/store`, `internal/inmems3/s3api`, and `internal/inmems3/zcopy` so the command entrypoint stays thin and server concerns are separated cleanly.
- Task 3 design update: load payload folders into the existing in-memory object store via a filesystem loader plus a gRPC control plane, so GET/HEAD/list and zcopy GET can serve those payloads immediately while PUT behavior remains unchanged until Task 4.
- Task 4 design update: keep GET backed by the loaded in-memory payload store, but route HTTP PUT and RDMA PUT through an ephemeral receive-and-discard ingest path that still validates size and returns an upload ETag.

## Findings From Initial Inspection

- The current implementation will not meet the requested long-term behavior because GET data only exists if a prior PUT stored it in memory.
- There is no configured payload-root concept yet.
- There are no server-side tests in the main repo covering HTTP S3 behavior or the zcopy server behavior.
- The current implementation likely works for basic create bucket, PUT, GET, HEAD, list, and zcopy demo flows, but this has not been verified yet with builds or tests.
- The current zcopy PUT path copies received bytes into Go memory before storing them, which is unnecessary for the desired discard-after-receive behavior.

## Proposed Tasks

- [x] Task 1: Build a baseline verification harness and document current behavior gaps.
- [x] Task 2: Refactor the server into `internal/` packages with no intended behavior change.
- [x] Task 3: Add a filesystem-backed payload source plus a gRPC control server for folder registration and cleanup.
- [x] Task 4: Change HTTP and RDMA PUT paths to receive, validate, and discard payloads instead of retaining objects for GET.
- [x] Task 5: Run verification, update docs/flags, and record results.

## Task Details

### Task 1

- Add focused tests around current HTTP S3 handler behavior and the store-facing logic we need to preserve.
- Identify any correctness problems that show up immediately during build/test.
- Record concrete gaps that must be addressed during refactor.

### Task 2

- Split startup/config, HTTP S3 API, payload/store abstractions, and RDMA zcopy service into maintainable packages.
- Keep the command entrypoint thin.
- Preserve the current client-facing behavior as much as possible before changing semantics.

### Task 3

- Add an optional startup payload-root preload path.
- Implement folder-tree loading where top-level directories become buckets and nested files become object keys.
- Load those payloads into the in-memory object store used by GET/HEAD/list and zcopy GET.
- Add a gRPC control server for operations such as add-folder, list-buckets, delete-bucket, and clear-all.

### Task 4

- Replace retained PUT object storage with transient receive-and-discard handling.
- Preserve request validation, size checks, and success/error responses.
- Remove unnecessary copies on the zcopy PUT path where possible.

### Task 5

- Run targeted tests and builds.
- Update README and any scripts/usage notes that need the new payload-root flow.
- Add a concise results section after implementation.

## Results

- Task 1 completed.
- Added baseline tests in [cmd/inmem-s3-server/main_test.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main_test.go) and [cmd/inmem-s3-server/zcopy_service_test.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/zcopy_service_test.go).
- Verified current default-build behavior with:
  `go test ./cmd/inmem-s3-server`
  `go test ./cmd/bench-client ./cmd/s3-rdma-zcopy-demo`
  `go test ./...`
- Verified that the default, non-`rdma` build is currently healthy in this environment.
- Attempted RDMA-tagged verification with:
  `CGO_ENABLED=1 go test -tags rdma ./cmd/inmem-s3-server`
- RDMA-tagged verification is currently blocked in this environment because the system header `infiniband/verbs.h` is missing.
- Baseline behaviors now covered by tests:
  HTTP PUT then GET round-trip
  HTTP oversized PUT rejection
  bucket/key path splitting for nested object keys
  zcopy PUT storing the received payload
  zcopy GET sending metadata plus payload at the requested shared-memory offset
- Remaining current-behavior gaps:
  no test coverage yet for full HTTP list/delete semantics
  no end-to-end RDMA verbs-path verification in this environment
  current semantics still retain PUT payloads, which will intentionally change in a later task
- Task 2 completed.
- Refactored the server into:
  [cmd/inmem-s3-server/main.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main.go)
  [internal/inmems3/app/app.go](/users/nehalem/rdma-demo/internal/inmems3/app/app.go)
  [internal/inmems3/store/store.go](/users/nehalem/rdma-demo/internal/inmems3/store/store.go)
  [internal/inmems3/s3api/handler.go](/users/nehalem/rdma-demo/internal/inmems3/s3api/handler.go)
  [internal/inmems3/zcopy/service.go](/users/nehalem/rdma-demo/internal/inmems3/zcopy/service.go)
- Moved the baseline tests next to the code they verify:
  [internal/inmems3/s3api/handler_test.go](/users/nehalem/rdma-demo/internal/inmems3/s3api/handler_test.go)
  [internal/inmems3/zcopy/service_test.go](/users/nehalem/rdma-demo/internal/inmems3/zcopy/service_test.go)
- Verification for Task 2:
  `go test ./...` passed after the refactor.
  `CGO_ENABLED=1 go test -tags rdma ./cmd/inmem-s3-server` is still blocked by missing `infiniband/verbs.h` in this environment, same as Task 1.
- Intended behavior for HTTP S3 and zcopy paths was preserved; Task 2 only changed structure and test placement.
- Task 3 completed.
- Added payload-folder loading in:
  [internal/inmems3/payload/loader.go](/users/nehalem/rdma-demo/internal/inmems3/payload/loader.go)
  [internal/inmems3/payload/loader_test.go](/users/nehalem/rdma-demo/internal/inmems3/payload/loader_test.go)
- Added the gRPC control plane in:
  [proto/inmems3/control/v1/control.proto](/users/nehalem/rdma-demo/proto/inmems3/control/v1/control.proto)
  [proto/inmems3/control/v1/control.pb.go](/users/nehalem/rdma-demo/proto/inmems3/control/v1/control.pb.go)
  [proto/inmems3/control/v1/control_grpc.pb.go](/users/nehalem/rdma-demo/proto/inmems3/control/v1/control_grpc.pb.go)
  [internal/inmems3/control/server.go](/users/nehalem/rdma-demo/internal/inmems3/control/server.go)
  [internal/inmems3/control/server_test.go](/users/nehalem/rdma-demo/internal/inmems3/control/server_test.go)
- Wired app startup to support:
  `--payload-root` for optional preload
  `--control-grpc-listen` for the control-plane listener
- Task 3 verification:
  `go test ./...` passed, including the new payload loader and gRPC control tests.
  `CGO_ENABLED=1 go test -tags rdma ./cmd/inmem-s3-server` is still blocked by missing `infiniband/verbs.h` in this environment, same as Tasks 1 and 2.
- Behavioral outcome of Task 3:
  payload folders can now be loaded into the in-memory object store for GET/HEAD/list and zcopy GET without changing PUT semantics yet.
- Post-Task verification update after environment fix:
  `CGO_ENABLED=1 go test -tags rdma ./cmd/inmem-s3-server` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  The earlier RDMA verification blocker was environmental rather than a code issue.
- Task 4 completed.
- Added the ephemeral PUT ingest helper in:
  [internal/inmems3/ingest/put.go](/users/nehalem/rdma-demo/internal/inmems3/ingest/put.go)
- Changed HTTP PUT in:
  [internal/inmems3/s3api/handler.go](/users/nehalem/rdma-demo/internal/inmems3/s3api/handler.go)
  so uploads are received, size-validated, hashed for the response ETag, and discarded instead of being retained for GET.
- Changed RDMA PUT in:
  [internal/inmems3/zcopy/service.go](/users/nehalem/rdma-demo/internal/inmems3/zcopy/service.go)
  so zero-copy uploads are validated and discarded without storing object bodies or copying them into the payload store.
- Updated regression coverage in:
  [internal/inmems3/s3api/handler_test.go](/users/nehalem/rdma-demo/internal/inmems3/s3api/handler_test.go)
  [internal/inmems3/zcopy/service_test.go](/users/nehalem/rdma-demo/internal/inmems3/zcopy/service_test.go)
- Task 4 verification:
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
- Behavioral outcome of Task 4:
  PUT no longer makes objects readable later.
  Preloaded payload objects remain the source of truth for GET/HEAD/list and zcopy GET.
- Task 5 completed.
- Updated repo-facing docs and helper flows in:
  [README.md](/users/nehalem/rdma-demo/README.md)
  [scripts/prepare_tcp_bucket_object.py](/users/nehalem/rdma-demo/scripts/prepare_tcp_bucket_object.py)
  [scripts/boto3_tcp_smoke.py](/users/nehalem/rdma-demo/scripts/boto3_tcp_smoke.py)
  [scripts/deploy_remote_server.sh](/users/nehalem/rdma-demo/scripts/deploy_remote_server.sh)
  [scripts/run_cross_host_tcp_sdk_demo.sh](/users/nehalem/rdma-demo/scripts/run_cross_host_tcp_sdk_demo.sh)
- Documentation updates now explain:
  payload-root preload
  gRPC control-plane usage
  PUT-discard semantics
  benchmark GET key requirements
  remote deploy flags for payload preload and control gRPC
- Task 5 verification:
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  `go build ./cmd/...` passed.
  `CGO_ENABLED=1 go build -tags rdma ./cmd/...` passed.
  `python3 -m py_compile scripts/boto3_tcp_smoke.py scripts/prepare_tcp_bucket_object.py` passed.
  `bash -n scripts/deploy_remote_server.sh scripts/run_cross_host_tcp_sdk_demo.sh` passed.
- Manual smoke verification:
  generated a workspace-local payload tree with `scripts/prepare_tcp_bucket_object.py`
  started `cmd/inmem-s3-server` with `--payload-root`
  confirmed a preloaded GET returned `200`
  confirmed a new PUT returned `200`
  confirmed GET on that uploaded key returned `404`

# Server Flag Handling And Config Documentation

## Goal

Make `cmd/inmem-s3-server` easier to launch as a standalone binary from another Go program, including tmux-based orchestration, by tightening its CLI flag handling and adding a dedicated reference for every supported config knob.

## Acceptance Criteria

- `inmem-s3-server --help` clearly explains the available listeners, defaults, and which flags are optional or disabled by default.
- Config validation rejects obviously bad values with actionable errors before the server starts listening.
- RDMA-related flags document when `0` means "use library default" versus when a value is required.
- The repo has a dedicated config reference that explains each server flag, its default, and when to set it.
- README points readers to the dedicated config reference and includes at least one launch example suitable for binary-orchestrated deployment.
- Verification covers the updated config/flag surface plus the normal Go test/build path.

## Working Notes

- The current binary uses the standard library `flag` package directly from [main.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main.go) with no custom usage output.
- Defaults currently exist in [app.go](/users/nehalem/rdma-demo/internal/inmems3/app/app.go), but the intent of several zero values is only implied in code comments or README examples.
- This follow-up should stay narrowly focused on CLI ergonomics and documentation rather than changing the server's transport or object semantics.

## Proposed Tasks

- [x] Task A: Add explicit config normalization, validation, and clearer help/usage output for `cmd/inmem-s3-server`.
- [x] Task B: Add a dedicated server configuration reference and update README entrypoints/examples.
- [x] Task C: Run tests/builds, verify the help output, and record results.

## Results

- Added a dedicated flag/config surface in:
  [internal/inmems3/app/app.go](/users/nehalem/rdma-demo/internal/inmems3/app/app.go)
  [cmd/inmem-s3-server/main.go](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main.go)
- The server binary now:
  prints grouped `--help` output
  validates bad config values before starting listeners
  normalizes the RDMA listen address when RDMA is enabled but the address was left unset programmatically
  keeps the help banner stable when launched through `go run` by showing the binary basename instead of a temp build path
- Added focused config/CLI tests in:
  [internal/inmems3/app/app_test.go](/users/nehalem/rdma-demo/internal/inmems3/app/app_test.go)
- Added a dedicated operator-facing config reference in:
  [docs/inmem-s3-server-config.md](/users/nehalem/rdma-demo/docs/inmem-s3-server-config.md)
- Updated the top-level entry docs in:
  [README.md](/users/nehalem/rdma-demo/README.md)
- Documentation now covers:
  every runtime flag
  defaults and zero-value semantics
  recommended launch profiles
  tmux-oriented binary launching
- Verification:
  `go test ./internal/inmems3/app` passed.
  `go run ./cmd/inmem-s3-server --help` printed the new grouped help text.
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  `go build ./cmd/inmem-s3-server` passed.
  `go build ./cmd/...` passed.
