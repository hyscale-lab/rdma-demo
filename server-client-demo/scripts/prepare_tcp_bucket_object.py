#!/usr/bin/env python3
"""Create bucket and upload one object to the in-memory S3 server (TCP path).

Edit the CONFIG section below, then run:
  python3 scripts/prepare_tcp_bucket_object.py
"""

from __future__ import annotations

from dataclasses import dataclass
import sys

import boto3
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError


# ===================== CONFIG (edit here) =====================
ENDPOINT = "http://10.0.1.2:10090"
REGION = "us-east-1"
ACCESS_KEY = "test"
SECRET_KEY = "test"

BUCKET = "bench-bucket"
KEY = "seed-object-1mb"
OBJECT_SIZE = 1_000_000

# payload mode: "pattern", "zeros", "random"
PAYLOAD_MODE = "pattern"
# =============================================================


@dataclass
class AppConfig:
    endpoint: str
    region: str
    access_key: str
    secret_key: str
    bucket: str
    key: str
    object_size: int
    payload_mode: str


def build_client(cfg: AppConfig):
    return boto3.client(
        "s3",
        endpoint_url=cfg.endpoint,
        region_name=cfg.region,
        aws_access_key_id=cfg.access_key,
        aws_secret_access_key=cfg.secret_key,
        config=Config(
            signature_version="s3v4",
            s3={"addressing_style": "path"},
            retries={"max_attempts": 3, "mode": "standard"},
        ),
    )


def ensure_bucket(s3, cfg: AppConfig) -> None:
    create_args = {"Bucket": cfg.bucket}
    if cfg.region != "us-east-1":
        create_args["CreateBucketConfiguration"] = {
            "LocationConstraint": cfg.region,
        }

    try:
        s3.create_bucket(**create_args)
        print(f"[ok] bucket created: {cfg.bucket}")
        return
    except ClientError as err:
        code = err.response.get("Error", {}).get("Code", "")
        if code in ("BucketAlreadyOwnedByYou", "BucketAlreadyExists"):
            print(f"[ok] bucket already exists: {cfg.bucket}")
            return
        raise


def make_payload(cfg: AppConfig) -> bytes:
    if cfg.object_size < 0:
        raise ValueError("OBJECT_SIZE must be >= 0")

    if cfg.payload_mode == "pattern":
        return bytes((i * 31 + 7) & 0xFF for i in range(cfg.object_size))
    if cfg.payload_mode == "zeros":
        return b"\x00" * cfg.object_size
    if cfg.payload_mode == "random":
        # Import lazily to avoid dependency at startup.
        import os

        return os.urandom(cfg.object_size)

    raise ValueError(f"invalid PAYLOAD_MODE={cfg.payload_mode!r}")


def put_and_verify(s3, cfg: AppConfig, payload: bytes) -> None:
    s3.put_object(
        Bucket=cfg.bucket,
        Key=cfg.key,
        Body=payload,
        ContentLength=len(payload),
    )
    head = s3.head_object(Bucket=cfg.bucket, Key=cfg.key)
    remote_size = int(head.get("ContentLength", -1))
    if remote_size != len(payload):
        raise RuntimeError(
            f"uploaded size mismatch: local={len(payload)} remote={remote_size}"
        )
    print(f"[ok] object uploaded: s3://{cfg.bucket}/{cfg.key} size={remote_size}")


def main() -> int:
    cfg = AppConfig(
        endpoint=ENDPOINT.strip(),
        region=REGION.strip(),
        access_key=ACCESS_KEY.strip(),
        secret_key=SECRET_KEY.strip(),
        bucket=BUCKET.strip(),
        key=KEY.strip(),
        object_size=OBJECT_SIZE,
        payload_mode=PAYLOAD_MODE.strip().lower(),
    )

    if not cfg.endpoint:
        print("ENDPOINT must not be empty", file=sys.stderr)
        return 2
    if not cfg.bucket:
        print("BUCKET must not be empty", file=sys.stderr)
        return 2
    if not cfg.key:
        print("KEY must not be empty", file=sys.stderr)
        return 2

    try:
        print(f"[info] endpoint={cfg.endpoint}")
        print(f"[info] bucket={cfg.bucket} key={cfg.key} size={cfg.object_size}")
        s3 = build_client(cfg)
        ensure_bucket(s3, cfg)
        payload = make_payload(cfg)
        put_and_verify(s3, cfg, payload)
        print("[done] bucket/object preparation finished")
        return 0
    except (ClientError, BotoCoreError, ValueError, RuntimeError) as err:
        print(f"[error] {err}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
