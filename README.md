# cam-rover-hub

## Landscape control deck

The camera fills the viewport, with a hold-to-drive pad at bottom left and a
relative-path minimap at bottom right. The top-right settings button opens
connection, minimap and drive/display tabs. Settings include a persisted private
LAN IP or `.local` rover address, stopped-state reconnect, Wi-Fi recovery,
minimap visibility/size/opacity/follow mode and camera fit. Map/display preferences
stay in the browser; rover addresses persist in the hub. Credentials are never
stored in browser preferences. A fullscreen button requests landscape when the
browser supports it; portrait layout remains usable.

Offline video disables driving but not settings or Stop. Activate manual control
explicitly before driving; pointer release/cancel, focus loss and hidden tabs
stop motion. Recovery never replays a held command. The default displayed speed
85 is sent when manual control is activated. Automatic driving remains subject
to `AUTO_ENABLED` and hardware acceptance; a minimap is not an obstacle map.

Raspberry Pi hub for the ESP32-CAM rover. The Gin server provides one mobile entry point for manual driving, rover Wi-Fi provisioning, and status. The service binds to loopback so it can be published privately with Tailscale Serve.

See [Pi deployment and hardware acceptance](docs/deployment.md) before exposing or moving the rover. Production requires a specific Tailscale identity and the service refuses non-loopback binds. Automatic movement is off until physical tests pass.

## Requirements

- Go 1.25 or newer
- Raspberry Pi 5 with Ethernet to the home router and a NetworkManager Wi-Fi profile named `cam-rover` for the rover AP
- Matching `ROVER_API_TOKEN` in hub environment and ESP32 firmware for authenticated rover mutations

## Run

```sh
go run .
```

The server listens on `127.0.0.1:8080` by default. `LISTEN_ADDR` may change the port but must retain `127.0.0.1`. `ROVER_ADDR` defaults to `cam-rover.local`; set it to the rover's home Wi-Fi IP if mDNS is unavailable. Set `ROVER_API_TOKEN` to the firmware token. The hub never stores the home Wi-Fi password in SQLite.

```sh
curl http://localhost:8080/healthz
```

Expected response: `{"status":"ok"}`.

## Current API

- `GET /ws`: same-origin WebSocket; commands are `{"type":"drive","direction":"forward"}`, `{"type":"speed","speed":170}`, `{"type":"light","on":true}`, and `{"type":"stop"}`. Hold-to-drive clients repeat the drive command every 250 ms. A lost owner stops within 550 ms; the ESP32 keeps its separate 700 ms deadman stop.
- `GET /api/status`, `GET /api/network`, `GET /api/wifi/scan`: hub and firmware status.
- `POST /api/network`: JSON `{ "mode":"sta", "ssid":"...", "password":"..." }` or `{ "mode":"sta" }` for saved credentials; `{ "mode":"ap" }` switches to the rover AP.
- `POST /api/wifi/scan`: start the firmware's Wi-Fi scan.
- `POST /api/link/ap`: connect the Pi's Wi-Fi interface to the preconfigured `cam-rover` NetworkManager profile, then use `192.168.71.1` for the rover.
- `GET /video.mjpeg`: a bounded, same-origin MJPEG feed. The hub keeps one upstream connection to ESP32 port 81 and drops stale frames for slow viewers.
- `POST /api/mode`: JSON `{ "mode":"manual"|"auto", "operator_id":N }`. The WebSocket sends the operator ID in a `hello` event. The browser renews a supervision heartbeat every 250 ms while visible and focused. Automatic mode stays unavailable until the vision and exploration worker report ready; a disconnected operator or video fault disarms it.
- `GET /api/maps`, `GET /api/maps/status`, `GET /api/maps/:id/poses`: saved map metadata, active SLAM status and scale-free path.
- `POST /api/maps` with `{ "name":"...", "operator_id":N }`: start a map job. `POST /api/maps/save` and `POST /api/maps/:id/load` use `{ "operator_id":N }` and stop motion before changing the job.

See [mapping setup](docs/mapping.md) for camera calibration, the separate stella_vslam worker, and Pi-specific checks. Selecting automatic mode with no saved map starts a mapping job but **does not drive the rover**. Loading a saved map likewise waits for relocalization and later safety approval.

`AUTO_ENABLED` defaults to off. Only set `AUTO_ENABLED=1` after the documented Pi/rover acceptance checks. The experimental runner makes short minimum-speed forward pulses and stops on visual uncertainty; it is not a certified collision-avoidance or obstacle-steering system.

NetworkManager profile activation needs permission for the service account. Create the profile on the Pi before using the AP button. Do not place the AP password in the hub database or shell command arguments. Normal driving uses the rover's home Wi-Fi address; the AP path is for provisioning and recovery.
