#!/usr/bin/env python3
"""Prepare a local payload tree for the in-memory S3 server.

Examples:
  python3 scripts/prepare_tcp_bucket_object.py \
    --root ./payload-root \
    --bucket nexus-benchmark-payload \
    --key input_payload/mapper/part-00000.csv \
    --size 1048576

  python3 scripts/prepare_tcp_bucket_object.py \
    --root ./payload-root \
    --bucket bench-bucket \
    --key-prefix tcp-sdk-demo \
    --count 1000 \
    --size 262144
"""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import sys


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Create a payload tree where top-level folders are buckets and nested files are object keys."
    )
    parser.add_argument(
        "--root",
        default="./payload-root",
        help="Payload root directory written by this script",
    )
    parser.add_argument("--bucket", default="bench-bucket", help="Bucket directory name")
    parser.add_argument(
        "--key",
        default="seed-object-1mb",
        help="Single object key to create when --count=1 and --key-prefix is not used",
    )
    parser.add_argument(
        "--key-prefix",
        default="",
        help="Generate benchmark-style keys <prefix>-00000000 .. when set",
    )
    parser.add_argument(
        "--count",
        type=int,
        default=1,
        help="Number of objects to generate when using --key-prefix",
    )
    parser.add_argument(
        "--start-index",
        type=int,
        default=0,
        help="Starting index for generated benchmark-style keys",
    )
    parser.add_argument("--size", type=int, default=1_000_000, help="Payload size in bytes")
    parser.add_argument(
        "--payload-mode",
        choices=("pattern", "zeros", "random"),
        default="pattern",
        help="Payload generation mode",
    )
    parser.add_argument(
        "--overwrite",
        action="store_true",
        help="Overwrite existing files instead of failing",
    )
    return parser.parse_args()


def make_payload(size: int, payload_mode: str) -> bytes:
    if size < 0:
        raise ValueError("size must be >= 0")

    if payload_mode == "pattern":
        return bytes((i * 31 + 7) & 0xFF for i in range(size))
    if payload_mode == "zeros":
        return b"\x00" * size
    if payload_mode == "random":
        return os.urandom(size)
    raise ValueError(f"invalid payload mode: {payload_mode!r}")


def object_key(prefix: str, index: int) -> str:
    return f"{prefix}-{index:08d}"


def target_keys(args: argparse.Namespace) -> list[str]:
    if args.key_prefix:
        if args.count <= 0:
            raise ValueError("count must be > 0 when using --key-prefix")
        if args.start_index < 0:
            raise ValueError("start-index must be >= 0")
        return [object_key(args.key_prefix, args.start_index + i) for i in range(args.count)]

    if args.count != 1:
        raise ValueError("count can only be > 1 when using --key-prefix")
    if not args.key:
        raise ValueError("key must not be empty")
    return [args.key]


def write_payload_file(path: Path, payload: bytes, overwrite: bool) -> None:
    if path.exists() and not overwrite:
        raise FileExistsError(f"{path} already exists (use --overwrite)")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(payload)


def main() -> int:
    args = parse_args()

    if not args.bucket:
        print("bucket must not be empty", file=sys.stderr)
        return 2

    try:
        payload = make_payload(args.size, args.payload_mode)
        keys = target_keys(args)
        root = Path(args.root).resolve()
        bucket_root = root / args.bucket

        print(f"[info] root={root}")
        print(f"[info] bucket={args.bucket} objects={len(keys)} size={args.size} mode={args.payload_mode}")

        for key in keys:
            out_path = bucket_root / Path(key)
            write_payload_file(out_path, payload, args.overwrite)

        print(f"[ok] wrote payload tree under {bucket_root}")
        if len(keys) == 1:
            print(f"[ok] object key: {keys[0]}")
        else:
            print(f"[ok] first key: {keys[0]}")
            print(f"[ok] last key:  {keys[-1]}")
        print("[done] payload preparation finished")
        return 0
    except (ValueError, OSError) as err:
        print(f"[error] {err}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
