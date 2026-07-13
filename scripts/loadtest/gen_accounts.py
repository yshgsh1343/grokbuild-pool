#!/usr/bin/env python3
"""Generate synthetic account NDJSON for Scheme2 import load tests."""

from __future__ import annotations

import argparse
import json
import time
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--count", type=int, default=140000)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--priority-base", type=int, default=100)
    args = p.parse_args()

    now = int(time.time())
    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("w", encoding="utf-8") as f:
        for i in range(args.count):
            row = {
                "id": f"acc-{i:06d}",
                "email": f"user{i}@example.invalid",
                "name": f"user{i}",
                "priority": args.priority_base + (i % 20),
                "access_token": f"access-{i:06d}",
                "refresh_token": f"refresh-{i:06d}",
                "expires_at": now + 3600 + (i % 1000),
                "proxy_mode": "",
                "proxy_url": "",
                "enabled": True,
                "lifecycle": "active",
            }
            f.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(f"wrote {args.count} accounts to {args.out}")


if __name__ == "__main__":
    main()
