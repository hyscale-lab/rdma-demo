#!/usr/bin/env python3
"""Smoke test for the in-memory S3 server TCP endpoint.

This verifies the current semantics:
- PUT uploads are accepted but do not become readable objects.
- Optional preloaded payload objects remain readable through GET/HEAD/list.
"""

import argparse
import sys
import time
import uuid

import boto3
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Run a boto3 smoke test for PUT-discard semantics and optional preloaded GET checks."
    )
    p.add_argument("--endpoint", default="http://10.0.1.2:10090", help="S3 endpoint URL")
    p.add_argument("--region", default="us-east-1", help="AWS region")
    p.add_argument("--access-key", default="test", help="Access key")
    p.add_argument("--secret-key", default="test", help="Secret key")
    p.add_argument("--bucket", default="", help="Bucket name (empty => auto-generate)")
    p.add_argument("--bucket-prefix", default="boto3-smoke", help="Auto bucket prefix")
    p.add_argument("--key", default="smoke-put-object", help="PUT test object key")
    p.add_argument("--size", type=int, default=4096, help="PUT payload size in bytes")
    p.add_argument(
        "--payload-mode",
        choices=("pattern", "zeros", "random"),
        default="pattern",
        help="PUT payload generation mode",
    )
    p.add_argument(
        "--verify-preloaded-bucket",
        default="",
        help="Optional bucket containing a preloaded payload object to read back",
    )
    p.add_argument(
        "--verify-preloaded-key",
        default="",
        help="Optional preloaded object key to GET/HEAD/list",
    )
    p.add_argument(
        "--verify-preloaded-size",
        type=int,
        default=-1,
        help="Expected size for the optional preloaded object",
    )
    p.add_argument(
        "--verify-preloaded-mode",
        choices=("pattern", "zeros", "random"),
        default="pattern",
        help="Expected payload mode for the optional preloaded object",
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


def make_payload(size: int, payload_mode: str) -> bytes:
    if size < 0:
        raise ValueError("size must be >= 0")
    if payload_mode == "pattern":
        return bytes((i * 31 + 7) & 0xFF for i in range(size))
    if payload_mode == "zeros":
        return b"\x00" * size
    if payload_mode == "random":
        import os

        return os.urandom(size)
    raise ValueError(f"invalid payload mode {payload_mode!r}")


def expect_not_found(callable_name: str, fn) -> None:
    try:
        fn()
    except ClientError as err:
        status = err.response.get("ResponseMetadata", {}).get("HTTPStatusCode")
        if status == 404:
            print(f"   {callable_name}=404 as expected")
            return
        raise
    raise RuntimeError(f"{callable_name} unexpectedly succeeded")


def main() -> int:
    args = parse_args()
    if args.size < 0:
        print("size must be >= 0", file=sys.stderr)
        return 2
    if bool(args.verify_preloaded_bucket) != bool(args.verify_preloaded_key):
        print(
            "verify-preloaded-bucket and verify-preloaded-key must be set together",
            file=sys.stderr,
        )
        return 2
    if args.verify_preloaded_key and args.verify_preloaded_size < 0:
        print("verify-preloaded-size must be >= 0 when verifying a preloaded object", file=sys.stderr)
        return 2

    bucket = args.bucket or make_bucket_name(args.bucket_prefix)
    key = args.key
    payload = make_payload(args.size, args.payload_mode)

    print(f"endpoint={args.endpoint}")
    print(f"bucket={bucket} key={key} size={args.size} mode={args.payload_mode}")

    s3 = build_client(args)

    try:
        print("1) create_bucket")
        maybe_create_bucket(s3, bucket, args.region)

        print("2) put_object")
        put_resp = s3.put_object(Bucket=bucket, Key=key, Body=payload, ContentLength=len(payload))
        print(f"   etag={put_resp.get('ETag')}")

        print("3) uploaded object should not be readable later")
        expect_not_found(
            "head_object",
            lambda: s3.head_object(Bucket=bucket, Key=key),
        )
        expect_not_found(
            "get_object",
            lambda: s3.get_object(Bucket=bucket, Key=key),
        )

        print("4) list_objects_v2 for uploaded key")
        listed = s3.list_objects_v2(Bucket=bucket, Prefix=key)
        count = listed.get("KeyCount", 0)
        print(f"   key_count={count}")
        if count != 0:
            print("expected uploaded key to be absent from list results", file=sys.stderr)
            return 1

        if args.verify_preloaded_key:
            verify_bucket = args.verify_preloaded_bucket
            verify_key = args.verify_preloaded_key
            print("5) verify preloaded object")
            head = s3.head_object(Bucket=verify_bucket, Key=verify_key)
            remote_size = int(head.get("ContentLength", -1))
            print(f"   content_length={remote_size}")
            if remote_size != args.verify_preloaded_size:
                print(
                    f"preloaded size mismatch: remote={remote_size} expected={args.verify_preloaded_size}",
                    file=sys.stderr,
                )
                return 1
            out = s3.get_object(Bucket=verify_bucket, Key=verify_key)
            data = out["Body"].read()
            expected = make_payload(args.verify_preloaded_size, args.verify_preloaded_mode)
            if data != expected:
                print("preloaded payload mismatch", file=sys.stderr)
                return 1
            listed = s3.list_objects_v2(Bucket=verify_bucket, Prefix=verify_key)
            print(f"   preloaded_key_count={listed.get('KeyCount', 0)}")

        if not args.no_cleanup:
            print("6) cleanup delete_bucket")
            s3.delete_object(Bucket=bucket, Key=key)
            s3.delete_bucket(Bucket=bucket)

        print("smoke test passed")
        return 0
    except (ClientError, BotoCoreError, RuntimeError, ValueError) as e:
        print(f"smoke test failed: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
