#!/usr/bin/env python3
"""Calibrate the rover's fixed camera from chessboard JPEGs and emit stella_vslam YAML.

Capture at the exact resolution and focus used for driving; move the board through
the whole image and several angles. The pattern dimensions are inner corners.
"""
import argparse
from pathlib import Path

import cv2
import numpy as np


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("images", type=Path, help="directory containing calibration JPEGs")
    parser.add_argument("output", type=Path, help="output stella_vslam camera YAML")
    parser.add_argument("--cols", type=int, default=9, help="inner corners across")
    parser.add_argument("--rows", type=int, default=6, help="inner corners down")
    args = parser.parse_args()
    if args.cols < 3 or args.rows < 3:
        parser.error("chessboard must have at least 3 by 3 inner corners")

    object_grid = np.zeros((args.rows * args.cols, 3), dtype=np.float32)
    object_grid[:, :2] = np.mgrid[0:args.cols, 0:args.rows].T.reshape(-1, 2)
    object_points, image_points = [], []
    size = None
    for path in sorted(args.images.glob("*.jpg")):
        image = cv2.imread(str(path))
        if image is None:
            continue
        height, width = image.shape[:2]
        if size is not None and size != (width, height):
            parser.error("all calibration images must have the same resolution")
        size = (width, height)
        gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
        found, corners = cv2.findChessboardCorners(gray, (args.cols, args.rows))
        if not found:
            continue
        corners = cv2.cornerSubPix(gray, corners, (11, 11), (-1, -1),
                                   (cv2.TERM_CRITERIA_EPS + cv2.TERM_CRITERIA_MAX_ITER, 30, 0.001))
        object_points.append(object_grid)
        image_points.append(corners)
    if len(image_points) < 15:
        parser.error(f"need at least 15 usable chessboard images; found {len(image_points)}")
    rms, camera, distortion, _, _ = cv2.calibrateCamera(object_points, image_points, size, None, None)
    if not np.isfinite(rms) or rms > 1.0:
        parser.error(f"calibration RMS {rms:.3f} px is too high; recapture images")
    k1, k2, p1, p2, k3 = distortion.ravel()[:5]
    width, height = size
    config = f'''# Generated from {len(image_points)} rover camera images; RMS {rms:.3f} px.
Camera:
  name: "rover-ov2640"
  setup: "monocular"
  model: "perspective"
  fx: {camera[0, 0]:.9f}
  fy: {camera[1, 1]:.9f}
  cx: {camera[0, 2]:.9f}
  cy: {camera[1, 2]:.9f}
  k1: {k1:.9f}
  k2: {k2:.9f}
  p1: {p1:.9f}
  p2: {p2:.9f}
  k3: {k3:.9f}
  fps: 5.0
  cols: {width}
  rows: {height}
  color_order: "BGR"
Feature:
  name: "rover ORB"
  scale_factor: 1.2
  num_levels: 8
  ini_fast_threshold: 20
  min_fast_threshold: 7
Mapping:
  baseline_dist_thr_ratio: 0.02
System:
  map_format: "msgpack"
'''
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(config, encoding="utf-8")
    print(f"wrote {args.output} from {len(image_points)} images; RMS {rms:.3f} px")


if __name__ == "__main__":
    main()
