# Lessons

- Failure mode: I tried to initialize an unexported SDK struct field from another package in a test.
- Detection signal: `go test ./cmd/inmem-s3-server` failed with `cannot refer to unexported field release in struct literal of type rdma.BorrowedMessage`.
- Prevention rule: when testing external package types, only use exported fields and methods or wrap them with a local fake instead of relying on internal fields.

- Failure mode: I used the raw executable path for CLI usage text, which made `go run ./cmd/inmem-s3-server --help` print a temporary build path instead of a stable binary name.
- Detection signal: manual help verification showed `/tmp/go-build.../exe/inmem-s3-server` in the usage banner.
- Prevention rule: use `filepath.Base(os.Args[0])`-style behavior for user-facing command names in CLI help text.

- Failure mode: I started adding package-level documentation files under code directories when the user wanted formal design/spec documentation centralized under `docs/`.
- Detection signal: the user explicitly corrected me to keep docs in the `docs` folder and use Go comments only inside code files when needed.
- Prevention rule: for this repo's new `s3-rdma-server` work, keep formal docs in `docs/` and avoid scattering standalone documentation files through package directories unless the user asks for that structure.

- Failure mode: I introduced a custom `FlagSet` abstraction for the new CLI when the user wanted the simplest possible `flag.Bool(...)/flag.Parse()` style in `main.go`.
- Detection signal: the user explicitly asked why the flags were wired that way and requested plain `flag.Bool(...)` plus `flag.Parse()`.
- Prevention rule: for this repo's new standalone binaries, prefer direct stdlib flag declarations in `main.go` unless there is a user-approved reason to add extra CLI abstraction.

- Failure mode: I treated connection shutdown as immediately invalidating all zcopy GET credits, which could drop an already-issued async GET before it sent `GetMeta` and the payload.
- Detection signal: the new `serveConn` Step 5 integration test only returned three control messages instead of the expected `hello_resp`, `resp_ok`, `resp_ok`, `get_meta` sequence.
- Prevention rule: when a protocol uses async sends plus credit accounting, preserve already-granted credits during graceful shutdown or cover that edge with a connection-sequence test.

- Failure mode: I left the standalone CLI help text describing old RDMA default semantics after the actual flag defaults had already been changed to the library defaults.
- Detection signal: while documenting the Step 6 tmux/runtime flow, the `usageText` banner still said `Default: 0 (library default ...)` even though the flags were initialized to concrete library-default values in `main.go`.
- Prevention rule: whenever flag defaults change, update the generated help banner and flag descriptions in the same edit so the CLI output matches the real runtime config.

- Failure mode: I initially tried to run the live RDMA smoke against `127.0.0.1`, which let the server bind but failed client route resolution at runtime.
- Detection signal: the Step 7 RDMA smoke returned `rdma_resolve_route: No such device` and `open rdma connection: context deadline exceeded` until I switched the endpoint to an active RDMA-backed netdev address.
- Prevention rule: for live RDMA verification, use `rdma link show` plus `ip addr show` to choose an active RDMA interface address instead of assuming loopback works.

- Failure mode: I destroyed the C RDMA listener before its accept workers had exited, while those workers could still be blocked in `rdma_get_cm_event`, which made server shutdown hang.
- Detection signal: the live `go run -tags rdma ./cmd/s3-rdma-server ...` repro logged `shutdown requested` on `SIGINT` but the process stayed alive until I sent a targeted `TERM`.
- Prevention rule: for CGO-backed listener shutdown paths, close the logical listener first, let workers wake and exit on a bounded accept timeout, and only then destroy the underlying C resources; keep a prompt-close regression test around that sequence.

- Failure mode: I mixed the stdlib `log` package into the `s3-rdma-server` zcopy path while the rest of the standalone server was using `logrus`, which made RDMA error logs bypass the configured formatter and look inconsistent.
- Detection signal: code inspection showed [internal/s3rdmaserver/zcopy/service.go](/users/nehalem/rdma-demo/internal/s3rdmaserver/zcopy/service.go) importing `log` while the server entrypoint and app lifecycle used `github.com/sirupsen/logrus`.
- Prevention rule: keep one logger stack per server path; when a standalone service is configured around `logrus`, avoid introducing stdlib `log` in adjacent components unless the output difference is intentional and documented.
