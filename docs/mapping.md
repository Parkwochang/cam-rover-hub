# Relative mapping on Pi 5

The Go service sends only the newest MJPEG frame to a separate `rover-vision`
process at at most 5 fps. The worker uses stella_vslam's monocular tracker and
map database. It emits relative camera X/Z poses; Go stores the path in SQLite
and draws a scale-free minimap. These coordinates are **not metres** and are
not an occupancy grid or a safety-rated obstacle detector.

1. Install and build stella_vslam on the Pi, including its vocabulary and
   OpenCV development files. Build this worker with
   `cmake -S vision -B vision/build && cmake --build vision/build -j2`.
2. Capture at least 15 diverse chessboard JPEGs from the mounted OV2640 at the
   exact stream resolution. Run `python3 vision/calibrate.py images/ config/camera.yaml`.
   The script needs `python3-opencv` and NumPy and refuses RMS above 1 px.
3. Set `VISION_WORKER`, `CAMERA_CONFIG`, `SLAM_VOCABULARY`, and `MAP_DIR` in the
   service environment. Use the compatible `orb_vocab.fbow` from stella_vslam.
4. Start a map in the UI, manually move slowly while supervised, then press
   save. A saved map can be loaded again, but tracking/relocalization must be
   observed before any later automatic movement is enabled.

Worker protocol: stdin repeats a 12-byte big-endian header (`uint32 JPEG size`,
`uint64 Unix nanoseconds`) followed by JPEG bytes, max 1 MiB. Stdout has one
JSON object per result (`pose` with x/y/heading or `lost`). EOF requests clean
shutdown and map save. Go kills a worker that fails to exit within 15 seconds.

The C++ worker and SLAM dependencies cannot be built or profiled on this
development Mac. Pi 5 acceptance requires checking camera calibration,
resolution, CPU/latency, relocalization, clean map save/reload, and emergency
stop on tracking/video/process failure. Until then, auto motion stays disabled.
