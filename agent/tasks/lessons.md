# Lessons

- Failure mode: I tried to initialize an unexported SDK struct field from another package in a test.
- Detection signal: `go test ./cmd/inmem-s3-server` failed with `cannot refer to unexported field release in struct literal of type rdma.BorrowedMessage`.
- Prevention rule: when testing external package types, only use exported fields and methods or wrap them with a local fake instead of relying on internal fields.

- Failure mode: I used the raw executable path for CLI usage text, which made `go run ./cmd/inmem-s3-server --help` print a temporary build path instead of a stable binary name.
- Detection signal: manual help verification showed `/tmp/go-build.../exe/inmem-s3-server` in the usage banner.
- Prevention rule: use `filepath.Base(os.Args[0])`-style behavior for user-facing command names in CLI help text.
