#!/usr/bin/env python3
"""Simple boto3 smoke test for the in-memory S3 server TCP endpoint."""

import argparse
import hashlib
import os
import sys
import tempfile
import time
import uuid
from pathlib import Path

import boto3
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Run a basic create/put/get/list/delete smoke test via boto3 over TCP."
    )
    p.add_argument("--endpoint", default="http://127.0.0.1:10090", help="S3 endpoint URL")
    p.add_argument("--region", default="us-east-1", help="AWS region")
    p.add_argument("--access-key", default="test", help="Access key")
    p.add_argument("--secret-key", default="test", help="Secret key")
    p.add_argument("--bucket", default="", help="Bucket name (empty => auto-generate)")
    p.add_argument("--bucket-prefix", default="boto3-smoke", help="Auto bucket prefix")
    p.add_argument("--key", default="smoke-object", help="Object key")
    p.add_argument("--size", type=int, default=4096, help="Payload size in bytes")
    p.add_argument(
        "--random-file",
        action="store_true",
        help="Generate a random local file, upload it, download it, and verify file checksum",
    )
    p.add_argument(
        "--tmp-dir",
        default="",
        help="Directory for generated files in random-file mode (default: system temp dir)",
    )
    p.add_argument(
        "--keep-files",
        action="store_true",
        help="Keep generated local files in random-file mode",
    )
    p.add_argument("--no-cleanup", action="store_true", help="Keep object and bucket after test")
    return p.parse_args()


def make_bucket_name(prefix: str) -> str:
    suffix = f"{int(time.time())}-{uuid.uuid4().hex[:8]}"
    return f"{prefix}-{suffix}".lower()


def build_client(args: argparse.Namespace):
    return boto3.client(
        "s3",
        endpoint_url=args.endpoint,
        region_name=args.region,
        aws_access_key_id=args.access_key,
        aws_secret_access_key=args.secret_key,
        config=Config(
            signature_version="s3v4",
            s3={"addressing_style": "path"},
            retries={"max_attempts": 3, "mode": "standard"},
        ),
    )


def maybe_create_bucket(s3, bucket: str, region: str) -> None:
    params = {"Bucket": bucket}
    if region != "us-east-1":
        params["CreateBucketConfiguration"] = {"LocationConstraint": region}
    s3.create_bucket(**params)


def sha256_path(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def write_random_file(path: Path, size: int) -> None:
    remaining = size
    with path.open("wb") as f:
        while remaining > 0:
            chunk = os.urandom(min(remaining, 1024 * 1024))
            f.write(chunk)
            remaining -= len(chunk)


def main() -> int:
    args = parse_args()
    if args.size < 0:
        print("size must be >= 0", file=sys.stderr)
        return 2

    bucket = args.bucket or make_bucket_name(args.bucket_prefix)
    key = args.key
    payload = bytes((i * 31 + 7) & 0xFF for i in range(args.size))
    upload_path = None
    download_path = None

    print(f"endpoint={args.endpoint}")
    print(f"bucket={bucket} key={key} size={args.size}")

    s3 = build_client(args)

    try:
        print("1) create_bucket")
        maybe_create_bucket(s3, bucket, args.region)

        print("2) put_object")
        if args.random_file:
            tmp_base = Path(args.tmp_dir) if args.tmp_dir else Path(tempfile.gettempdir())
            tmp_base.mkdir(parents=True, exist_ok=True)
            stamp = f"{int(time.time())}-{uuid.uuid4().hex[:8]}"
            upload_path = tmp_base / f"boto3-upload-{stamp}.bin"
            download_path = tmp_base / f"boto3-download-{stamp}.bin"
            write_random_file(upload_path, args.size)
            with upload_path.open("rb") as f:
                s3.put_object(Bucket=bucket, Key=key, Body=f, ContentLength=args.size)
            print(f"   upload_file={upload_path}")
        else:
            s3.put_object(Bucket=bucket, Key=key, Body=payload, ContentLength=len(payload))

        print("3) head_object")
        head = s3.head_object(Bucket=bucket, Key=key)
        print(f"   content_length={head.get('ContentLength')}")

        print("4) get_object")
        out = s3.get_object(Bucket=bucket, Key=key)
        data = out["Body"].read()
        if args.random_file:
            assert download_path is not None
            with download_path.open("wb") as f:
                f.write(data)
            src_hash = sha256_path(upload_path)
            dst_hash = sha256_path(download_path)
            print(f"   upload_sha256={src_hash}")
            print(f"   download_sha256={dst_hash}")
            if src_hash != dst_hash:
                print("file checksum mismatch", file=sys.stderr)
                return 1
        else:
            if data != payload:
                print("payload mismatch", file=sys.stderr)
                return 1

        print("5) list_objects_v2")
        listed = s3.list_objects_v2(Bucket=bucket, Prefix=key)
        count = listed.get("KeyCount", 0)
        print(f"   key_count={count}")

        if not args.no_cleanup:
            print("6) cleanup delete_object/delete_bucket")
            s3.delete_object(Bucket=bucket, Key=key)
            s3.delete_bucket(Bucket=bucket)

        if args.random_file and not args.keep_files:
            if upload_path is not None and upload_path.exists():
                upload_path.unlink()
            if download_path is not None and download_path.exists():
                download_path.unlink()

        print("smoke test passed")
        return 0
    except (ClientError, BotoCoreError) as e:
        print(f"smoke test failed: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
