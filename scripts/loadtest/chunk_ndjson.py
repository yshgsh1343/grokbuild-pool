#!/usr/bin/env python3
"""Split NDJSON into fixed-size chunk files for import jobs."""

from __future__ import annotations

import argparse
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--in", dest="inp", type=Path, required=True)
    p.add_argument("--out-dir", type=Path, required=True)
    p.add_argument("--chunk-size", type=int, default=5000)
    args = p.parse_args()

    args.out_dir.mkdir(parents=True, exist_ok=True)
    chunk_idx = 0
    n_in_chunk = 0
    out = None
    total = 0
    with args.inp.open("r", encoding="utf-8") as f:
        for line in f:
            if not line.strip():
                continue
            if out is None or n_in_chunk >= args.chunk_size:
                if out is not None:
                    out.close()
                chunk_idx += 1
                n_in_chunk = 0
                out = (args.out_dir / f"chunk-{chunk_idx:05d}.ndjson").open("w", encoding="utf-8")
            out.write(line if line.endswith("\n") else line + "\n")
            n_in_chunk += 1
            total += 1
    if out is not None:
        out.close()
    print(f"split {total} rows into {chunk_idx} chunks at {args.out_dir}")


if __name__ == "__main__":
    main()
