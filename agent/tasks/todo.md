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

# AWS SDK RDMA Cleanup Investigation

## Goal

Investigate the local `aws-sdk-go-v2` RDMA modifications since commit `d9b5cbfba1bb279c7422081d115812ab53583963` and identify any leftover code from the abandoned generic HTTP-over-RDMA approach that is no longer needed for the current zero-copy `s3rdmaclient` design.

## Acceptance Criteria

- Trace the high-level evolution from the early generic S3/HTTP RDMA dialer path to the current zero-copy path.
- Distinguish active code required by the current repo from legacy-looking public surface that can confuse future users.
- Produce an explicit report with file/line references and concrete cleanup options.

## Working Notes

- Commit `9d58608f686` added S3 client HTTP transport integration for an RDMA dialer and listener support.
- Commit `4c770eeac82` later added the dedicated zero-copy `s3rdmaclient` and `zcopyproto` path.
- Current repo usage appears to go through `aws/transport/http/rdma/s3rdmaclient` rather than the generic `service/s3` RDMA dialer toggles.
- `aws/transport/http/client.go`'s `WithDialContext` hook predates the RDMA work and is not itself a RDMA-specific leftover.

## Proposed Tasks

- [x] Compare the RDMA-related SDK history after `d9b5cbf...` to identify first-generation vs current design pieces.
- [x] Search the repo for actual usage of the generic S3 RDMA transport toggles versus `s3rdmaclient`.
- [x] Capture the findings with exact file/line references and concrete cleanup options.

## Results

- The active benchmark and smoke flows in this repo use `aws/transport/http/rdma/s3rdmaclient`, `PutZeroCopy`, `GetZeroCopy`, and `EnsureBucket`; there is no non-test repo usage of the generic `service/s3` RDMA transport toggles.
- The most likely leftover artifact is the public S3 client RDMA transport surface in:
  [aws-sdk-go-v2/service/s3/options.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/options.go)
  [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go)
  [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go)
- The low-level transport pieces that remain necessary for the current design include:
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/conn.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/conn.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go)
  because they provide the RDMA message transport, listener, borrowed receive, and offset-send capabilities used by `s3rdmaclient` and the server.

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

# `s3-rdma-server` Rebuild

## Goal

Build a new, standalone `s3-rdma-server` that is intentionally smaller than `inmem-s3-server`, keeps the minimal in-memory benchmark semantics, supports both TCP and RDMA zcopy, discards all client PUT payloads after validation, and only keeps startup-loaded GET objects in memory.

## Acceptance Criteria

- The new implementation lives under `cmd/s3-rdma-server`, `internal/s3rdmaserver`, `pkg/s3rdmasmoke`, and `cmd/s3-rdma-smoke`.
- The new server does not import `internal/inmems3/*` or reference `inmem-s3-server` in names, docs, or package paths.
- TCP keeps the current minimal S3-compatible benchmark behavior.
- RDMA implements only the zcopy protocol needed by the modified `aws-sdk-go-v2` in this repo.
- PUT succeeds after validation and payload receipt, then discards the object body on both TCP and RDMA.
- GET/HEAD/list read only from startup-loaded `--payload-root` data.
- The binary runs as a standalone CLI process suitable for tmux.
- A combined smoke tool can validate both TCP and RDMA behavior.

## Working Notes

- `cmd/s3-rdma-server/main.go` currently exists only as a stub with comments indicating that real config constants and flag parsing should be added there.
- User explicitly wants this implementation to be isolated enough to move to a separate repository later.
- User explicitly wants review and approval after every step in the implementation plan.
- Step 1 is docs only: create the spec first and the design diagrams second, then stop.
- The chosen product decisions are:
  startup-only `--payload-root`
  no runtime admin or control API
  no object retention for PUT
  one combined smoke tool for both TCP and RDMA

## Proposed Steps

- [x] Step 1: Create the `s3-rdma-server` project spec and architecture diagrams.
- [x] Step 2: Build the new project skeleton and CLI contract.
- [x] Step 3: Implement the simple in-memory GET store and startup loader.
- [x] Step 4: Implement the minimal S3/TCP handler.
- [x] Step 5: Implement the RDMA zcopy service compatible with the current SDK.
- [x] Step 6: Wire the standalone app lifecycle and CLI behavior.
- [x] Step 7: Add the combined TCP + RDMA smoke tool.
- [x] Step 8: Run final verification and repo cleanup.

## Results

- Step 1 completed.
- Added the implementation spec in:
  [agent/specs/s3-rdma-server.md](/users/nehalem/rdma-demo/agent/specs/s3-rdma-server.md)
- Added the architecture and flow diagrams in:
  [docs/s3-rdma-server-architecture.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-architecture.md)
- Step 1 verification:
  document review only
  no code or behavior changes beyond the spec and design artifacts
- Step 2 completed.
- Added the new standalone entrypoint in:
  [cmd/s3-rdma-server/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go)
- Added the new app-level runtime/config skeleton in:
  [internal/s3rdmaserver/app/app.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/app/app.go)
- Added the package skeleton files in:
  [internal/s3rdmaserver/store/store.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/store/store.go)
  [internal/s3rdmaserver/s3api/handler.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/s3api/handler.go)
  [internal/s3rdmaserver/zcopy/service.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service.go)
  [pkg/s3rdmasmoke/smoke.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke.go)
- Step 2 behavior:
  the new binary now has its own flag surface, grouped `--help`, and RDMA tuning defaults
  the CLI uses plain `flag.Bool` / `flag.String` / `flag.Parse()` in `cmd/s3-rdma-server/main.go`, and the flag defaults now live there instead of in `app`
  the app layer is intentionally still a skeleton and only handles config validation plus startup logging
  no `internal/inmems3/*` package is imported by the new `s3-rdma-server` path
- Step 2 verification:
  `gofmt -w cmd/s3-rdma-server/main.go internal/s3rdmaserver/app/app.go internal/s3rdmaserver/store/store.go internal/s3rdmaserver/s3api/handler.go internal/s3rdmaserver/zcopy/service.go pkg/s3rdmasmoke/smoke.go` passed.
  `go build ./cmd/s3-rdma-server` passed.
  `go run ./cmd/s3-rdma-server --help` printed the grouped CLI help text.
- Step 2 note:
  `go build ./cmd/s3-rdma-server` created an untracked workspace-local binary named `s3-rdma-server`.
- Step 3 completed.
- Added the minimal GET object store in:
  [internal/s3rdmaserver/store/store.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/store/store.go)
- Added the startup payload-root loader in:
  [internal/s3rdmaserver/store/loader.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/store/loader.go)
- Added focused Step 3 unit tests in:
  [internal/s3rdmaserver/store/store_test.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/store/store_test.go)
  [internal/s3rdmaserver/store/loader_test.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/store/loader_test.go)
- Step 3 behavior:
  startup-loaded objects now store body bytes, ETag, and LastModified in a simple bucket/object map
  bucket listing and object listing are sorted for stable output
  the loader maps top-level directories to buckets and nested files to slash-normalized object keys
  non-directory entries under the payload root are ignored
  invalid or empty payload roots fail fast in the loader
- Step 3 verification:
  `gofmt -w internal/s3rdmaserver/store/store.go internal/s3rdmaserver/store/loader.go internal/s3rdmaserver/store/store_test.go internal/s3rdmaserver/store/loader_test.go` passed.
  `go test ./internal/s3rdmaserver/store` passed.
  `go build ./cmd/s3-rdma-server` passed.
- Step 4 completed.
- Added the minimal S3/TCP handler in:
  [internal/s3rdmaserver/s3api/handler.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/s3api/handler.go)
- Added focused Step 4 handler tests in:
  [internal/s3rdmaserver/s3api/handler_test.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/s3api/handler_test.go)
- Step 4 behavior:
  bucket-level PUT, HEAD, GET list, DELETE, and root bucket listing are now implemented
  object-level PUT, GET, HEAD, and DELETE are now implemented
  HTTP PUT streams the request body, computes the response ETag, enforces `max-object-size`, creates the bucket if needed, and discards the payload
  GET, HEAD, and list read only from the startup-loaded store
  the handler returns S3-style XML errors and request-id headers like the current benchmark server
- Step 4 verification:
  `gofmt -w internal/s3rdmaserver/s3api/handler.go internal/s3rdmaserver/s3api/handler_test.go` passed.
  `go test ./internal/s3rdmaserver/s3api` passed.
  `go build ./cmd/s3-rdma-server` passed.
- Step 5 completed.
- Added the standalone RDMA zcopy service in:
  [internal/s3rdmaserver/zcopy/service.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service.go)
- Added focused Step 5 zcopy tests in:
  [internal/s3rdmaserver/zcopy/service_test.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service_test.go)
- Step 5 behavior:
  the new service now implements the `HelloReq`, `HelloResp`, `Ack`, `EnsureBucketReq`, `PutReq`, `GetReq`, `GetMeta`, `RespOK`, and `RespErr` control flow expected by the local `s3rdmaclient`
  RDMA PUT validates the request, receives the borrowed payload at the requested shared-memory offset, creates the bucket if needed, and discards the uploaded object body instead of retaining it
  RDMA GET reads only from the startup-loaded in-memory store, enforces the requested `max` size, sends `GetMeta`, and then sends the payload at the requested shared-memory offset
  the zcopy connection now preserves already-granted GET credits during shutdown so an in-flight async GET can finish cleanly before the connection closes
- Step 5 verification:
  `gofmt -w internal/s3rdmaserver/zcopy/service.go internal/s3rdmaserver/zcopy/service_test.go` passed.
  `go test ./internal/s3rdmaserver/zcopy` passed.
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  `go build ./cmd/s3-rdma-server` passed.
- Step 5 focused protocol coverage:
  direct zcopy unit tests now prove PUT returns `RespOK` without storing uploaded objects
  direct zcopy unit tests now prove PUT does not overwrite preloaded GET objects
  direct zcopy unit tests now prove GET sends `GetMeta` plus payload at the requested shared-memory offset
  an end-to-end `serveConn` fake-transport test now proves the `HelloReq` -> `EnsureBucketReq` -> `PutReq` -> `GetReq` control flow in one connection sequence
- Step 6 completed.
- Replaced the Step 2 runtime skeleton in:
  [internal/s3rdmaserver/app/app.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/app/app.go)
- Added the standalone run/ops notes in:
  [docs/s3-rdma-server-operations.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-operations.md)
- Step 6 behavior:
  the standalone binary now preloads `--payload-root` before opening listeners
  TCP starts when `--tcp-listen` is non-empty and uses the new minimal S3 handler
  RDMA zcopy starts only when `--enable-rdma-zcopy` is set and uses the new standalone zcopy service
  the process listens for `SIGINT` and `SIGTERM`, logs the shutdown reason, and closes listeners cleanly
  startup logs now include the effective runtime config plus the actual bound listener addresses, which works well for tmux-managed launches
- Step 6 verification:
  `gofmt -w internal/s3rdmaserver/app/app.go` passed.
  `gofmt -w cmd/s3-rdma-server/main.go` passed.
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  `go build ./cmd/...` passed.
  `go build ./cmd/s3-rdma-server` passed.
  `go run ./cmd/s3-rdma-server --help` passed and now reports the actual RDMA flag defaults.
  standalone runtime smoke passed:
  `./s3-rdma-server --tcp-listen 127.0.0.1:0`
  then `SIGINT` produced a clean `shutdown requested` log and exit code `0`
  tmux runtime smoke passed with the documented launch pattern in `docs/s3-rdma-server-operations.md`
  startup under tmux showed a bound TCP listener address and `tmux send-keys ... C-c` produced a clean shutdown log in the preserved pane
- Step 7 completed.
- Added the shared smoke library in:
  [pkg/s3rdmasmoke/smoke.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke.go)
- Added focused Step 7 smoke-tool tests in:
  [pkg/s3rdmasmoke/smoke_test.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke_test.go)
- Added the standalone smoke CLI in:
  [cmd/s3-rdma-smoke/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-smoke/main.go)
- Added the smoke usage notes in:
  [docs/s3-rdma-smoke.md](/users/nehalem/rdma-demo/docs/s3-rdma-smoke.md)
- Step 7 behavior:
  `s3-rdma-smoke` now supports `--transport=tcp|rdma`, `--op=put|get|put-get`, shared common flags, and RDMA-specific shared-memory plus tuning flags
  the TCP path uses the standard S3 client against the standalone server's minimal S3 API
  the RDMA path uses `s3rdmaclient` with an mmap-backed shared-memory region and the standalone zcopy protocol
  `put-get` now verifies the intended benchmark semantic that uploaded objects are accepted and then unreadable because the server discards them
  `get` now verifies the startup-loaded readable object path for both transports
- Step 7 verification:
  `gofmt -w pkg/s3rdmasmoke/smoke.go pkg/s3rdmasmoke/smoke_test.go cmd/s3-rdma-smoke/main.go` passed.
  `go test ./pkg/s3rdmasmoke ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  `go build ./cmd/s3-rdma-smoke ./cmd/s3-rdma-server` passed.
  `go build ./cmd/...` passed.
  live TCP smoke passed with:
  `/tmp/s3-rdma-smoke-rdma --transport tcp --endpoint http://127.0.0.1:10090 --bucket smoke-bucket --key preloaded.txt --op get --payload-size 7`
  `/tmp/s3-rdma-smoke-rdma --transport tcp --endpoint http://127.0.0.1:10090 --bucket smoke-bucket --key tcp-putget.bin --op put-get --payload-size 1024`
  live RDMA smoke passed after retrying on an active RDMA interface address instead of loopback:
  `/tmp/s3-rdma-smoke-rdma --transport rdma --endpoint 10.0.1.1:10191 --bucket smoke-bucket --key preloaded.txt --op get --payload-size 7`
  `/tmp/s3-rdma-smoke-rdma --transport rdma --endpoint 10.0.1.1:10191 --bucket smoke-bucket --key rdma-putget.bin --op put-get --payload-size 1024`
  live RDMA binaries for manual verification were built with:
  `CGO_ENABLED=1 go build -tags rdma -o /tmp/s3-rdma-server-rdma ./cmd/s3-rdma-server`
  `CGO_ENABLED=1 go build -tags rdma -o /tmp/s3-rdma-smoke-rdma ./cmd/s3-rdma-smoke`
  the first live RDMA attempt on `127.0.0.1:10191` failed with route-resolution errors, which was an environment/addressing issue rather than a smoke-tool logic failure
- Step 8 completed.
- Updated the top-level docs to center the standalone deliverable:
  [README.md](/users/nehalem/rdma-demo/README.md)
  [docs/s3-rdma-server-config.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-config.md)
  [docs/s3-rdma-server-operations.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-operations.md)
  [docs/s3-rdma-smoke.md](/users/nehalem/rdma-demo/docs/s3-rdma-smoke.md)
- Step 8 behavior and cleanup:
  the top-level README now documents only the standalone `s3-rdma-server` and `s3-rdma-smoke` workflow instead of the older `inmem-s3-server` control-plane flow
  the new config reference documents every standalone server flag and its practical use
  the operations and smoke docs now call out that live RDMA should use an active RDMA-backed interface address instead of loopback
  the temporary verification payload tree `.tmp-s3-rdma-smoke` and the untracked root-level `s3-rdma-server` build artifact were removed from the repo worktree
- Step 8 verification:
  `gofmt -w cmd/s3-rdma-server/main.go cmd/s3-rdma-smoke/main.go` passed.
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  `go build ./cmd/...` passed.
  `go run ./cmd/s3-rdma-server --help` passed.
  `go run ./cmd/s3-rdma-smoke --help` passed.
  RDMA-enabled binaries for final live verification were built with:
  `CGO_ENABLED=1 go build -tags rdma -o /tmp/s3-rdma-server-rdma ./cmd/s3-rdma-server`
  `CGO_ENABLED=1 go build -tags rdma -o /tmp/s3-rdma-smoke-rdma ./cmd/s3-rdma-smoke`
  final manual startup with `--payload-root` passed on fresh ports:
  `/tmp/s3-rdma-server-rdma --tcp-listen 127.0.0.1:10091 --enable-rdma-zcopy --rdma-zcopy-listen 10.0.1.1:10192 --payload-root /users/nehalem/rdma-demo/.tmp-s3-rdma-smoke`
  final manual TCP smoke passed with:
  `/tmp/s3-rdma-smoke-rdma --transport tcp --endpoint http://127.0.0.1:10091 --bucket smoke-bucket --key preloaded.txt --op get --payload-size 7`
  final manual RDMA smoke passed with:
  `/tmp/s3-rdma-smoke-rdma --transport rdma --endpoint 10.0.1.1:10192 --bucket smoke-bucket --key rdma-putget-final.bin --op put-get --payload-size 1024`
  the final live server shut down cleanly on `SIGINT`

## Current Focus

- [x] Rebuild plan complete.

# Remove Legacy RDMA-Aware HTTP Dialer

## Goal

Remove the abandoned generic HTTP-over-RDMA path from the local `aws-sdk-go-v2` fork while preserving the currently working zero-copy RDMA flow used by `s3-rdma-server`, `s3-rdma-smoke`, and `s3rdmaclient`.

## Acceptance Criteria

- The standard `service/s3` client no longer exposes or documents the legacy RDMA-aware HTTP dialing knobs.
- The generic TCP/HTTP SDK path matches upstream as closely as possible while preserving the current zero-copy RDMA implementation.
- The zero-copy RDMA path through `aws/transport/http/rdma/s3rdmaclient` still works without API or behavior regressions.
- `s3-rdma-server` still serves TCP and RDMA zcopy traffic.
- `s3-rdma-smoke` still passes in both TCP and RDMA modes.
- No repo docs or comments imply that the standard S3 HTTP client is the supported zero-copy RDMA path.

## Working Notes

- Current active RDMA users in this repo go through:
  [aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go)
  [internal/s3rdmaserver/app/app.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/app/app.go)
  [cmd/s3-rdma-zcopy-demo/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-zcopy-demo/main.go)
  [pkg/s3rdmasmoke/smoke.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke.go)
- The legacy-looking public surface is concentrated in:
  [aws-sdk-go-v2/service/s3/options.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/options.go)
  [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go)
  [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/dialer.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/dialer.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/conn.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/conn.go)
- `NewVerbsMessageListener` is still required by the active server path.
- `BorrowedMessage`, `BorrowingMessageConn`, `MessageConn`, and offset-send support are still required by the active zero-copy path.
- Against commit `16dc9bb90743290f48664dafc6dea07f5486cd0f`, the remaining generic SDK deltas are mainly:
  [aws-sdk-go-v2/aws/transport/http/client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/client.go)
  [aws-sdk-go-v2/aws/transport/http/client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/client_test.go)
  and minor formatting/toolchain noise in:
  [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go)
  [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go)
  [aws-sdk-go-v2/go.mod](/users/nehalem/rdma-demo/aws-sdk-go-v2/go.mod)

## Proposed Tasks

- [x] Task 1: Remove legacy RDMA transport knobs from the S3 client surface.
- [x] Task 2: Remove or quarantine the generic RDMA HTTP bridge types that are no longer needed by zero-copy.
- [x] Task 3: Update comments/docs so `s3rdmaclient` is the only documented RDMA client path.
- [x] Task 4: Revert the remaining generic TCP/HTTP SDK modifications that are no longer needed.
- [x] Task 5: Run full TCP and RDMA verification against the current `s3-rdma-server` implementation.

## Task Details

### Task 1

- Delete from [aws-sdk-go-v2/service/s3/options.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/options.go):
  `EnableRDMATransport`
  `RDMADialer`
  `WithEnableRDMATransport`
  `WithRDMADialer`
- Delete from [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go):
  `AWS_S3_RDMA_ENABLED`
  `AWS_S3_RDMA_DISABLE_FALLBACK`
  `finalizeRDMATransportHTTPClient`
  `resolveRDMATransportDialer`
  `isRDMATransportEnabled`
  `resolveBoolEnv` if it becomes unused
- Delete the legacy S3 RDMA tests from [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go).

### Task 2

- Remove [aws-sdk-go-v2/aws/transport/http/rdma/dialer.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/dialer.go) if nothing outside the deleted S3 path still depends on it.
- Remove `NewVerbsDialer` from [aws-sdk-go-v2/aws/transport/http/rdma/verbs.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs.go) if the dialer is removed.
- Remove `NewVerbsListener` from [aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go) if you want zero-copy-only message listeners.
- Remove the `net.Conn` adapter pieces from [aws-sdk-go-v2/aws/transport/http/rdma/conn.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/conn.go) only if they become fully unused:
  `Conn`
  `NewConn`
  stream-style `Read`/`Write`/deadline logic
- Keep the message-oriented pieces used by zero-copy:
  `MessageConn`
  `BorrowedMessage`
  `BorrowingMessageConn`
  `MessageListener`

### Task 3

- Update package comments and docs so the supported RDMA client path is:
  [aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go)
- Make sure README/docs for the standalone server and smoke tool do not mention the removed S3 RDMA HTTP dialer path.
- Add a short note near the RDMA transport package if needed explaining that the message-oriented API is the supported surface for zero-copy.

### Task 4

- Revert [aws-sdk-go-v2/aws/transport/http/client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/client.go) and [aws-sdk-go-v2/aws/transport/http/client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/client_test.go) to upstream if the current repo does not need:
  `BuildableClient.WithDialContext`
  `BuildableClient.CloseIdleConnections`
  the added `dialContext` clone behavior
  the added `suppressBadHTTPRedirectTransport.CloseIdleConnections`
- Revert generic S3 formatting-only drift in:
  [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go)
  [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go)
- Decide whether to keep or drop the fork-local toolchain line in:
  [aws-sdk-go-v2/go.mod](/users/nehalem/rdma-demo/aws-sdk-go-v2/go.mod)
  based on whether merge-minimization or toolchain pinning is more important.
- Keep the RDMA-specific subtree intact:
  [aws-sdk-go-v2/aws/transport/http/rdma](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma)

### Task 5

- Run:
  `go test ./...`
  `CGO_ENABLED=1 go test -tags rdma ./...`
  `go build ./cmd/...`
- Re-run live smoke against the current server:
  TCP `s3-rdma-smoke --transport tcp`
  RDMA `s3-rdma-smoke --transport rdma`
- Re-check:
  [cmd/s3-rdma-zcopy-demo/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-zcopy-demo/main.go)
  [cmd/s3-rdma-server/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go)
  [pkg/s3rdmasmoke/smoke.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke.go)
  to confirm no removed API is still referenced.

## Results

- Task 1 completed.
- Removed the legacy S3 RDMA client surface from:
  [aws-sdk-go-v2/service/s3/options.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/options.go)
  [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go)
  [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go)
- Deleted:
  `EnableRDMATransport`
  `RDMADialer`
  `WithEnableRDMATransport`
  `WithRDMADialer`
  `AWS_S3_RDMA_ENABLED`
  `AWS_S3_RDMA_DISABLE_FALLBACK`
  `finalizeRDMATransportHTTPClient`
  `resolveRDMATransportDialer`
  `isRDMATransportEnabled`
  `resolveBoolEnv`
  and the matching legacy S3 RDMA tests.
- Verified no S3-facing legacy RDMA symbols remain with:
  `rg -n "EnableRDMATransport|WithEnableRDMATransport|WithRDMADialer|RDMADialer|AWS_S3_RDMA_ENABLED|AWS_S3_RDMA_DISABLE_FALLBACK" aws-sdk-go-v2`
- Task 1 verification:
  `gofmt -w aws-sdk-go-v2/service/s3/options.go aws-sdk-go-v2/service/s3/api_client.go aws-sdk-go-v2/service/s3/api_client_test.go` passed.
  `GOTOOLCHAIN=go1.26.1 go test ./...` passed in [aws-sdk-go-v2/service/s3](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3).
  `go test ./...` passed at repo root.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed at repo root.
  `go build ./cmd/s3-rdma-server ./cmd/s3-rdma-smoke` passed.
- Verification note:
  the nested `aws-sdk-go-v2/service/s3` module tries to auto-download `go1.23` by default, which is not available as a toolchain tag in this environment, so the focused module test was run with `GOTOOLCHAIN=go1.26.1` to use the already-installed local toolchain.
- Task 2 completed.
- Removed the legacy RDMA HTTP bridge layer from:
  [aws-sdk-go-v2/aws/transport/http/rdma/conn.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/conn.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/dialer.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/dialer.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_stub.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_stub.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go)
- Deleted the old stream-adapter and dialer tests in:
  [aws-sdk-go-v2/aws/transport/http/rdma/conn_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/conn_test.go)
  and removed the `NewVerbsDialer` test from [aws-sdk-go-v2/aws/transport/http/rdma/verbs_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_test.go).
- The RDMA transport package now keeps only the message-oriented zero-copy surface:
  `MessageConn`
  `BorrowedMessage`
  `BorrowingMessageConn`
  `MessageListener`
  `VerbsOptions.Open`
  `NewVerbsMessageListener`
- Removed:
  `Dialer`
  `NewVerbsDialer`
  `NewVerbsListener`
  the `Conn` / `NewConn` net.Conn adapter
  the `verbsListener.Accept() (net.Conn, error)` path
- Downstream compatibility cleanup:
  [cmd/bench-client/main.go](/users/nehalem/rdma-demo/cmd/bench-client/main.go) had dead flags tied to the removed dialer bridge (`allow-fallback`, `rdma-open-parallelism`, `rdma-open-min-interval`), so those references were removed to keep the repo building cleanly. This did not change active behavior because `bench-client` already rejects `mode=rdma`.
- Verified no bridge symbols remain in the RDMA transport package with:
  `rg -n "NewConn\\(|type Conn struct|NewVerbsDialer\\(|NewVerbsListener\\(|\\bDialer\\b|newVerbsListener\\(" aws-sdk-go-v2/aws/transport/http/rdma`
- Task 2 verification:
  `gofmt -w aws-sdk-go-v2/aws/transport/http/rdma/conn.go aws-sdk-go-v2/aws/transport/http/rdma/verbs.go aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go aws-sdk-go-v2/aws/transport/http/rdma/verbs_stub.go aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go aws-sdk-go-v2/aws/transport/http/rdma/verbs_test.go` passed.
  `gofmt -w cmd/bench-client/main.go` passed.
  `GOTOOLCHAIN=go1.26.1 go test ./aws/transport/http/rdma` passed in [aws-sdk-go-v2](/users/nehalem/rdma-demo/aws-sdk-go-v2).
  `GOTOOLCHAIN=go1.26.1 CGO_ENABLED=1 go test -tags rdma ./aws/transport/http/rdma` passed in [aws-sdk-go-v2](/users/nehalem/rdma-demo/aws-sdk-go-v2).
  `go test ./...` passed at repo root.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed at repo root.
  `go build ./cmd/s3-rdma-server ./cmd/s3-rdma-smoke ./cmd/s3-rdma-zcopy-demo` passed.
- Task 3 completed.
- Updated repo docs so the supported client mapping is explicit:
  TCP uses the standard `service/s3` client
  RDMA uses `s3rdmaclient`
  there is no supported RDMA path through the standard `service/s3` client
- Updated docs in:
  [README.md](/users/nehalem/rdma-demo/README.md)
  [docs/s3-rdma-server-architecture.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-architecture.md)
  [docs/s3-rdma-server-config.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-config.md)
  [docs/s3-rdma-server-operations.md](/users/nehalem/rdma-demo/docs/s3-rdma-server-operations.md)
  [docs/s3-rdma-smoke.md](/users/nehalem/rdma-demo/docs/s3-rdma-smoke.md)
- Added code comments that point future readers at the supported zero-copy RDMA surfaces in:
  [aws-sdk-go-v2/aws/transport/http/rdma/doc.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/doc.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/conn.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/conn.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go)
  [aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go)
- Task 3 verification:
  `gofmt -w aws-sdk-go-v2/aws/transport/http/rdma/doc.go aws-sdk-go-v2/aws/transport/http/rdma/conn.go aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener.go aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient/client.go` passed.
  `rg -n 'RDMA-aware HTTP|HTTP-over-RDMA|NewVerbsDialer|NewVerbsListener|allow-fallback|rdma-open-parallelism|rdma-open-min-interval' README.md docs aws-sdk-go-v2/aws/transport/http/rdma --glob '!**/*_test.go'` returned no matches.
  `go test ./...` passed at repo root.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed at repo root.
- Task 4 completed.
- Reverted the remaining generic TCP/HTTP SDK modifications in:
  [aws-sdk-go-v2/aws/transport/http/client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/client.go)
  [aws-sdk-go-v2/aws/transport/http/client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/client_test.go)
  [aws-sdk-go-v2/go.mod](/users/nehalem/rdma-demo/aws-sdk-go-v2/go.mod)
  [aws-sdk-go-v2/service/s3/api_client.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client.go)
  [aws-sdk-go-v2/service/s3/api_client_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3/api_client_test.go)
- Removed no-longer-needed generic SDK changes:
  `BuildableClient.WithDialContext`
  `BuildableClient.CloseIdleConnections`
  the extra `dialContext` clone state
  the `suppressBadHTTPRedirectTransport.CloseIdleConnections` forwarding
  the fork-local `toolchain` line in the nested AWS SDK `go.mod`
- The RDMA-specific subtree under
  [aws-sdk-go-v2/aws/transport/http/rdma](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma)
  was left untouched during Task 4 so the current zero-copy client/server path remains isolated from this merge-minimization cleanup.
- Verified the generic cleanup is not referenced by the current repo with:
  `rg -n "WithDialContext\\(|CloseIdleConnections\\(|dialContext" aws-sdk-go-v2 cmd pkg internal`
- Task 4 verification:
  `gofmt -w aws-sdk-go-v2/aws/transport/http/client.go aws-sdk-go-v2/aws/transport/http/client_test.go aws-sdk-go-v2/service/s3/api_client.go aws-sdk-go-v2/service/s3/api_client_test.go` passed.
  `GOTOOLCHAIN=go1.26.1 go test ./aws/transport/http` passed in [aws-sdk-go-v2](/users/nehalem/rdma-demo/aws-sdk-go-v2).
  `GOTOOLCHAIN=go1.26.1 go test ./...` passed in [aws-sdk-go-v2/service/s3](/users/nehalem/rdma-demo/aws-sdk-go-v2/service/s3).
  `go test ./...` passed at repo root.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed at repo root.
  `go build ./cmd/s3-rdma-server ./cmd/s3-rdma-smoke ./cmd/s3-rdma-zcopy-demo` passed.
- Task 5 completed.
- Re-checked the current entrypoints for removed generic HTTP/RDMA bridge APIs:
  [cmd/s3-rdma-server/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go)
  [cmd/s3-rdma-zcopy-demo/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-zcopy-demo/main.go)
  [pkg/s3rdmasmoke/smoke.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke.go)
- Reference verification:
  `rg -n "WithDialContext\\(|CloseIdleConnections\\(|NewVerbsDialer\\(|NewVerbsListener\\(" cmd/s3-rdma-zcopy-demo/main.go cmd/s3-rdma-server/main.go pkg/s3rdmasmoke/smoke.go` returned no matches.
- Task 5 verification:
  `go test ./...` passed at repo root.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed at repo root.
  `go build ./cmd/...` passed at repo root.
  Created a temporary payload-root under `/tmp/s3-rdma-task5.GmIDtL` with one preloaded object:
  `s3://bench-bucket/preloaded/object.bin` size `4096`.
  Live server launch for smoke verification:
  `CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-server --tcp-listen 127.0.0.1:19090 --enable-rdma-zcopy --rdma-zcopy-listen 10.0.1.1:19191 --payload-root /tmp/s3-rdma-task5.GmIDtL`
  Live TCP smoke:
  `go run ./cmd/s3-rdma-smoke --transport tcp --endpoint 127.0.0.1:19090 --bucket bench-bucket --key preloaded/object.bin --op get --payload-size 4096` passed.
  `go run ./cmd/s3-rdma-smoke --transport tcp --endpoint 127.0.0.1:19090 --bucket bench-bucket --key task5-tcp-put-get.bin --op put-get --payload-size 4096` passed.
  Live RDMA smoke:
  `CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-smoke --transport rdma --endpoint 10.0.1.1:19191 --bucket bench-bucket --key preloaded/object.bin --op get --payload-size 4096` passed.
  `CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-smoke --transport rdma --endpoint 10.0.1.1:19191 --bucket bench-bucket --key task5-rdma-put-get.bin --op put-get --payload-size 4096` passed.
- Verification note:
  launching `s3-rdma-server` without `-tags rdma` fails cleanly with `rdma verbs backend unavailable`, so live RDMA verification must use an RDMA build and an RDMA-backed interface address rather than loopback.
- Residual note:
  during the live `go run -tags rdma ./cmd/s3-rdma-server ...` verification, the server logged `shutdown requested` on `SIGINT` but did not exit promptly; the exact verification process tree had to be stopped with a targeted `TERM`. The TCP and RDMA smoke results were still successful, but RDMA shutdown/teardown likely needs another look.

# RDMA Shutdown Path Fix

## Goal

Fix the standalone RDMA server shutdown hang so `s3-rdma-server` exits promptly on `SIGINT`/`SIGTERM` when the RDMA zcopy listener is enabled.

## Acceptance Criteria

- `app.Run` returns promptly after shutdown is requested with the RDMA listener enabled.
- The RDMA listener close path no longer blocks indefinitely when no incoming connection is waking `AcceptMessage`.
- Regression coverage exists for the listener shutdown sequencing.
- Verification includes `go test ./...`, `CGO_ENABLED=1 go test -tags rdma ./...`, and a live `go run -tags rdma ./cmd/s3-rdma-server ...` shutdown repro.

## Working Notes

- The observed hang happened after successful live TCP and RDMA smoke traffic, during shutdown of `go run -tags rdma ./cmd/s3-rdma-server`.
- `internal/s3rdmaserver/app/app.go` blocks on `zcopySrv.Close()`, so if RDMA listener close hangs, process exit hangs.
- `aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go` currently calls `go_rdma_listener_close()` before waiting for accept workers to exit, and the C-side close can race a blocking `rdma_get_cm_event`.

## Proposed Tasks

- [x] Reproduce and localize the RDMA shutdown hang in the listener close path.
- [x] Fix the RDMA listener shutdown ordering so accept workers can exit cleanly.
- [x] Add regression coverage for prompt close behavior.
- [x] Run tests and live shutdown verification.
- [x] Summarize the fix and record any new lesson.

## Results

- Root cause:
  [aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_linux_cgo.go)
  destroyed the C RDMA listener before its accept workers had exited. Those workers were blocked in `rdma_get_cm_event`, so `verbsListener.Close()` could hang during shutdown.
- Fix:
  changed the RDMA accept path to poll the listener event channel with a short timeout and treat idle waits as a normal timeout instead of a hard error.
  changed `verbsListener.Close()` to mark the listener closed, wait for accept workers to exit, and only then destroy the C listener resources.
- Regression coverage:
  added [verbs_listener_test.go](/users/nehalem/rdma-demo/aws-sdk-go-v2/aws/transport/http/rdma/verbs_listener_test.go)
  with `TestVerbsMessageListenerClosePrompt`, which creates a real RDMA message listener and fails if `Close()` does not return within 2 seconds.
- Verification:
  `GOTOOLCHAIN=go1.26.1 CGO_ENABLED=1 go test -tags rdma ./aws/transport/http/rdma -run TestVerbsMessageListenerClosePrompt -count=1` passed.
  `GOTOOLCHAIN=go1.26.1 CGO_ENABLED=1 go test -tags rdma ./aws/transport/http/rdma` passed.
  `go test ./...` passed at repo root.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed at repo root.
  `go build ./cmd/s3-rdma-server ./cmd/s3-rdma-smoke ./cmd/s3-rdma-zcopy-demo` passed.
  live repro:
  started `CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-server --tcp-listen 127.0.0.1:19094 --enable-rdma-zcopy --rdma-zcopy-listen 10.0.1.1:19195 --payload-root /tmp/s3-rdma-shutdown-fix.qS3fn0`
  confirmed TCP and RDMA `get` smoke both passed
  sent `SIGINT`
  verified the process exited promptly and no matching `s3-rdma-server` process remained.

# Smoke Failure Investigation

## Goal

Explain and fix why the current `s3-rdma-smoke` TCP and RDMA commands are failing against the expected local server endpoints.

## Acceptance Criteria

- We can explain the current `connection refused` TCP error and the RDMA connect rejection.
- If there is a code/config bug, it is fixed at the root cause.
- Live smoke succeeds again for both TCP and RDMA against the intended server process.
- Results and any new lesson are recorded.

## Proposed Tasks

- [x] Inspect the current server runtime state and reproduce the reported TCP/RDMA smoke failures.
- [x] Isolate and fix the root cause in the server or smoke path.
- [x] Add regression coverage or documentation updates if needed.
- [x] Re-run live smoke verification for TCP and RDMA.
- [x] Summarize the result and record any new lesson.

## Results

- Root cause:
  there was no running [s3-rdma-server](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go) or [inmem-s3-server](/users/nehalem/rdma-demo/cmd/inmem-s3-server/main.go) process, and nothing was listening on `127.0.0.1:10090` or `10.0.1.1:10191`.
- Evidence:
  `ps -ef | grep '[s]3-rdma-server\|[i]nmem-s3-server'` returned no server process.
  `ss -ltnp '( sport = :10090 or sport = :10191 )'` showed no listeners on the smoke ports.
- Interpretation:
  the TCP failure was expected because the endpoint was closed (`connection refused`).
  the RDMA failure was also consistent with "no usable server listener at that endpoint"; in this environment the client retried and surfaced a CM connect rejection (`RDMA_CM_EVENT_REJECTED status=8`) rather than a TCP-style refused socket error.
- Verification repro:
  created a temporary payload root under `/tmp/s3-rdma-smoke-check.hRjL3p` containing `s3://smoke-bucket/preloaded.txt` with 7 bytes.
  started `CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-server --tcp-listen 127.0.0.1:10090 --enable-rdma-zcopy --rdma-zcopy-listen 10.0.1.1:10191 --payload-root /tmp/s3-rdma-smoke-check.hRjL3p`
  reran the exact user commands:
  `./s3-rdma-smoke --transport rdma --endpoint 10.0.1.1:10191 --bucket smoke-bucket --key preloaded.txt --op get --payload-size 7` passed.
  `./s3-rdma-smoke --transport tcp --endpoint http://127.0.0.1:10090 --bucket smoke-bucket --key preloaded.txt --op get --payload-size 7` passed.
- Outcome:
  no code change was needed for the smoke path itself; the issue was that the expected server was not running on those endpoints.

# Server Logging Investigation

## Goal

Understand why `s3-rdma-server` logs are not printing properly and fix the logging path if there is a real formatting or consistency bug.

## Acceptance Criteria

- We can explain the current logging behavior of `s3-rdma-server`.
- If logs are inconsistent or malformed, they are fixed at the root cause.
- Verification includes a live server run that shows the intended log output.

## Proposed Tasks

- [x] Inspect current logging usage in the standalone server and RDMA path.
- [x] Reproduce the current log output and identify the root cause.
- [x] Implement the smallest logging fix if needed.
- [x] Re-run a live server launch and verify the logs.
- [x] Summarize the result and record any lesson if applicable.

## Results

- Root cause:
  [internal/s3rdmaserver/app/app.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/app/app.go)
  and [cmd/s3-rdma-server/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go)
  use `logrus`, but [internal/s3rdmaserver/zcopy/service.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service.go)
  was still importing the stdlib `log` package and emitting raw `Printf` lines for RDMA zcopy errors.
- Effect:
  startup and lifecycle logs used the configured `logrus` formatter, while zcopy error logs would bypass that configuration and print in a different style.
- Fix:
  switched the zcopy service logging to `logrus` and used structured error logging for:
  zcopy init failure
  zcopy PUT failure
  zcopy GET scheduling failure
  async zcopy GET failure with `req_id`
- Regression coverage:
  added a focused test in [internal/s3rdmaserver/zcopy/service_test.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service_test.go)
  that forces a zcopy init failure and verifies the emitted line contains `logrus`-style `level=error` output.
- Verification:
  `gofmt -w internal/s3rdmaserver/zcopy/service.go internal/s3rdmaserver/zcopy/service_test.go` passed.
  `go test ./internal/s3rdmaserver/zcopy` passed.
  `go test ./...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
  live runtime check:
  started `CGO_ENABLED=1 go run -tags rdma ./cmd/s3-rdma-server --tcp-listen 127.0.0.1:10096 --enable-rdma-zcopy --rdma-zcopy-listen 10.0.1.1:10197 --payload-root /tmp/s3-rdma-log-check.aUCVLx`
  confirmed startup and shutdown logs still print through the normal `logrus` path.

# RDMA Module Relocation Import Fix

## Goal

Resolve all broken imports after moving the RDMA code out of the local AWS SDK override into `pkg/rdma`.

## Acceptance Criteria

- All stale imports that still point at the old AWS SDK RDMA package path are updated to the new `pkg/rdma` module path.
- The root module and any nested modules build and test again after the relocation.
- Any related module wiring needed for the new `pkg/rdma/go.mod` layout is corrected.

## Proposed Tasks

- [x] Inspect the new module layout and list all stale RDMA import paths.
- [x] Update code imports and any affected module requirements/replaces.
- [x] Fix any remaining compile or test failures from the relocation.
- [x] Run verification and summarize the result.

## Results

- Updated stale RDMA imports from the old AWS SDK path to the relocated module:
  `github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma`
  -> `github.com/hyscale-lab/rdma-demo/rdma`
  `github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma/s3rdmaclient`
  -> `github.com/hyscale-lab/rdma-demo/rdma/client`
  `github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma/zcopyproto`
  -> `github.com/hyscale-lab/rdma-demo/rdma/zcopyproto`
- Updated stale root-module imports from:
  `rdma-demo/server-client-demo/...`
  to:
  `github.com/hyscale-lab/rdma-demo/...`
- Touched the command, server, smoke, and legacy in-memory server paths that still referenced the old module locations, including:
  [cmd/s3-rdma-server/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-server/main.go)
  [cmd/s3-rdma-smoke/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-smoke/main.go)
  [cmd/s3-rdma-zcopy-demo/main.go](/users/nehalem/rdma-demo/cmd/s3-rdma-zcopy-demo/main.go)
  [pkg/s3rdmasmoke/smoke.go](/users/nehalem/rdma-demo/pkg/s3rdmasmoke/smoke.go)
  [internal/s3rdmaserver/app/app.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/app/app.go)
  [internal/s3rdmaserver/zcopy/service.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service.go)
  [internal/inmems3/app/app.go](/users/nehalem/rdma-demo/internal/inmems3/app/app.go)
  [internal/inmems3/zcopy/service.go](/users/nehalem/rdma-demo/internal/inmems3/zcopy/service.go)
  and the related tests/helpers that imported those packages.
- Module graph fix:
  ran `go mod tidy`, which added the local `github.com/hyscale-lab/rdma-demo/rdma` requirement through the existing root `replace` and refreshed [go.sum](/users/nehalem/rdma-demo/go.sum) for the now-active dependency graph.
- Verification:
  `rg -n 'rdma-demo/server-client-demo|github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma|aws/transport/http/rdma/s3rdmaclient|aws/transport/http/rdma/zcopyproto' cmd internal pkg` returned no matches.
  `gofmt -w ...` passed on the touched files.
  `go mod tidy` passed.
  `go test ./...` passed.
  `go build ./cmd/...` passed.
  `CGO_ENABLED=1 go test -tags rdma ./...` passed.
