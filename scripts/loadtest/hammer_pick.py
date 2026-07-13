#!/usr/bin/env python3
"""Minimal HTTP hammer for gateway/worker bootstrap endpoints."""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import time
import urllib.error
import urllib.request
from collections import Counter


def one(url: str, sticky: str | None, timeout: float) -> tuple[int, float, str]:
    req = urllib.request.Request(url)
    if sticky:
        req.add_header("X-Sticky-Key", sticky)
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode("utf-8", "replace")
            return resp.status, (time.perf_counter() - t0) * 1000, body
    except urllib.error.HTTPError as e:
        return e.code, (time.perf_counter() - t0) * 1000, str(e)
    except Exception as e:  # noqa: BLE001
        return 0, (time.perf_counter() - t0) * 1000, str(e)


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--url", default="http://127.0.0.1:8081/internal/v1/pick")
    p.add_argument("--n", type=int, default=1000)
    p.add_argument("--concurrency", type=int, default=50)
    p.add_argument("--sticky-prefix", default="")
    p.add_argument("--timeout", type=float, default=5.0)
    args = p.parse_args()

    lat = []
    codes: Counter[int] = Counter()

    def task(i: int):
        sticky = f"{args.sticky_prefix}{i%100}" if args.sticky_prefix != "" else None
        return one(args.url, sticky, args.timeout)

    t0 = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as ex:
        for code, ms, _ in ex.map(task, range(args.n)):
            codes[code] += 1
            lat.append(ms)
    elapsed = time.perf_counter() - t0
    lat.sort()
    def pct(p: float) -> float:
        if not lat:
            return 0.0
        return lat[min(len(lat) - 1, int(len(lat) * p))]

    out = {
        "n": args.n,
        "concurrency": args.concurrency,
        "elapsed_sec": round(elapsed, 3),
        "qps": round(args.n / elapsed, 2) if elapsed else 0,
        "p50_ms": round(pct(0.50), 3),
        "p95_ms": round(pct(0.95), 3),
        "p99_ms": round(pct(0.99), 3),
        "codes": dict(codes),
    }
    print(json.dumps(out, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
