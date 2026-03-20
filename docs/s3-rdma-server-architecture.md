# `s3-rdma-server` Architecture

This document captures the target design for the new standalone `s3-rdma-server`.

## Design Goals

- keep the server compact and easy to move to a separate repository
- keep the TCP/S3 benchmark behavior already relied on by existing clients
- add only the RDMA zcopy path required by the modified SDK
- discard all uploaded `PUT` objects after successful receipt
- keep only startup-loaded `GET` objects in memory

## High-Level Components

```mermaid
flowchart LR
    Smoke["s3-rdma-smoke\n(tcp or rdma mode)"]
    SDK["AWS S3 SDK / s3rdmaclient"]
    Main["cmd/s3-rdma-server"]
    App["internal/s3rdmaserver/app"]
    HTTP["internal/s3rdmaserver/s3api"]
    ZC["internal/s3rdmaserver/zcopy"]
    Store["internal/s3rdmaserver/store"]
    Loader["startup payload loader"]
    Disk["payload-root folder tree"]

    Smoke --> SDK
    SDK --> Main
    Main --> App
    App --> HTTP
    App --> ZC
    App --> Loader
    Loader --> Disk
    Loader --> Store
    HTTP --> Store
    ZC --> Store
```

## TCP PUT / GET Flow

```mermaid
sequenceDiagram
    participant Client as TCP S3 client
    participant HTTP as HTTP handler
    participant Ingest as PUT ingest
    participant Store as GET store

    alt PUT /bucket/key
        Client->>HTTP: PUT object body
        HTTP->>Ingest: stream body
        Ingest->>Ingest: size check + MD5
        Ingest-->>HTTP: ETag + size
        HTTP->>Store: create bucket only
        HTTP-->>Client: 200 OK
        Note over Store: object body is not saved
    else GET /bucket/key
        Client->>HTTP: GET object
        HTTP->>Store: lookup preloaded object
        Store-->>HTTP: bytes + metadata
        HTTP-->>Client: 200 + payload
    end
```

Notes:

- TCP `PUT` never makes the uploaded object readable later.
- TCP `GET` only serves objects loaded from `--payload-root` at startup.

## RDMA PUT Flow

```mermaid
sequenceDiagram
    participant Client as s3rdmaclient
    participant ZC as zcopy service
    participant Transport as RDMA verbs transport
    participant Store as GET store

    Client->>ZC: HelloReq / credits
    ZC-->>Client: HelloResp
    Client->>ZC: PutReq(bucket, key, size, data_offset)
    Client->>Transport: RDMA write of payload
    Transport-->>ZC: borrowed payload view
    ZC->>ZC: validate bucket/key/size/offset
    ZC->>Store: create bucket only
    ZC->>ZC: discard payload after receipt
    ZC-->>Client: RespOK
```

Notes:

- RDMA is used only for the zcopy path.
- No non-zcopy RDMA API is implemented.
- The server does not persist uploaded PUT bytes.

## RDMA GET Flow

```mermaid
sequenceDiagram
    participant Client as s3rdmaclient
    participant ZC as zcopy service
    participant Store as GET store
    participant Transport as RDMA verbs transport

    Client->>ZC: GetReq(bucket, key, max, data_offset)
    ZC->>Store: lookup startup-loaded object
    Store-->>ZC: bytes + metadata
    ZC-->>Client: GetMeta(size, data_offset)
    ZC->>Transport: SendMessageAt(payload, data_offset)
    Transport-->>Client: payload into client shared memory
    Client-->>ZC: Ack
```

Notes:

- RDMA GET only serves startup-loaded objects.
- `max` is enforced before the payload send.
- Credits and `Ack` continue to follow the current client protocol expectations.

## Smoke Tool Interaction

```mermaid
flowchart TD
    Start["s3-rdma-smoke"]
    Mode{"--transport"}
    TCP["TCP mode\nAWS S3 client"]
    RDMA["RDMA mode\ns3rdmaclient + mmap shared memory"]
    Server["s3-rdma-server"]
    Verify["verify result semantics"]

    Start --> Mode
    Mode -->|tcp| TCP
    Mode -->|rdma| RDMA
    TCP --> Server
    RDMA --> Server
    Server --> Verify
```

Expected smoke-tool semantics:

- `put`: upload succeeds
- `get`: preloaded object is readable and optionally verified
- `put-get`: upload succeeds, but later `GET` of that uploaded key should fail unless the key was already preloaded

## Startup Data Loading

```mermaid
flowchart LR
    Root["--payload-root"]
    BucketDir["top-level directory = bucket"]
    File["nested file = object key"]
    Store["in-memory GET store"]

    Root --> BucketDir
    BucketDir --> File
    File --> Store
```

Rules:

- loading happens once before listeners start
- empty `--payload-root` means no preloaded readable objects
- there is no runtime reload API in v1
