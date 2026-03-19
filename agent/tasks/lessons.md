# Lessons

- Failure mode: I tried to initialize an unexported SDK struct field from another package in a test.
- Detection signal: `go test ./cmd/inmem-s3-server` failed with `cannot refer to unexported field release in struct literal of type rdma.BorrowedMessage`.
- Prevention rule: when testing external package types, only use exported fields and methods or wrap them with a local fake instead of relying on internal fields.
