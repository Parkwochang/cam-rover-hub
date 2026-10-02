#!/usr/bin/env python3
"""Feed recorded JPEG frames to the Pi vision worker for repeatable checks."""
import argparse
import json
from pathlib import Path
import struct
import subprocess
import threading
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("frames", type=Path, help="directory of ordered .jpg frames")
    parser.add_argument("map_file", type=Path, help="new output .msg file")
    parser.add_argument("--worker", type=Path, default=Path("vision/build/rover-vision"))
    parser.add_argument("--config", type=Path, default=Path("config/camera.yaml"))
    parser.add_argument("--vocab", type=Path, default=Path("config/orb_vocab.fbow"))
    args = parser.parse_args()
    frames = sorted(args.frames.glob("*.jpg"))
    if not frames:
        parser.error("no .jpg frames found")
    if args.map_file.exists() or args.map_file.is_symlink():
        parser.error("output map already exists; choose a new file")
    for path in frames:
        if not 0 < path.stat().st_size <= 1 << 20:
            parser.error(f"frame outside 1 MiB protocol limit: {path}")
    args.map_file.parent.mkdir(parents=True, exist_ok=True)

    counts = {"pose": 0, "lost": 0, "risk": 0, "safe": 0}
    process = subprocess.Popen(
        [str(args.worker), "--config", str(args.config), "--vocab", str(args.vocab), "--map", str(args.map_file)],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    )

    def read_results():
        for line in process.stdout:
            try:
                event = json.loads(line)
            except (ValueError, UnicodeDecodeError):
                continue
            if not isinstance(event, dict):
                continue
            kind = event.get("type")
            if kind in counts:
                counts[kind] += 1
            if kind == "risk" and event.get("safe"):
                counts["safe"] += 1

    reader = threading.Thread(target=read_results, daemon=True)
    reader.start()
    try:
        base = time.time_ns()
        for index, path in enumerate(frames):
            jpeg = path.read_bytes()
            process.stdin.write(struct.pack(">IQ", len(jpeg), base + index * 200_000_000))
            process.stdin.write(jpeg)
        process.stdin.close()
        code = process.wait(timeout=max(120, len(frames) // 5))
    except (BrokenPipeError, subprocess.TimeoutExpired):
        process.kill()
        process.wait()
        raise SystemExit("worker failed or timed out")
    reader.join(timeout=2)
    print(f"frames={len(frames)} pose={counts['pose']} lost={counts['lost']} risk={counts['risk']} low_risk={counts['safe']}")
    if code or counts["pose"] == 0 or counts["risk"] == 0:
        raise SystemExit("replay failed: inspect video, calibration and worker output")


if __name__ == "__main__":
    main()
