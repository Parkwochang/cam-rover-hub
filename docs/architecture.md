# Architecture

The mobile browser reaches the Gin server through Tailscale Serve. The Gin server is the only process that talks to the ESP32 HTTP API and MJPEG endpoint. A single control coordinator owns motor commands. A bounded frame broker distributes the latest JPEG to browser clients and the vision worker. SQLite stores map metadata and relative pose samples; vision map files live outside SQLite. The vision worker never sends motor commands.

The Pi uses Ethernet to reach the home router. Its Wi-Fi interface joins the rover AP only for initial provisioning or recovery. During normal driving the hub uses the rover's home Wi-Fi address.

Safety invariant: any missing operator heartbeat, rover connection, video stream, or visual tracking signal makes the coordinator send stop and prevents automatic restart.

The vision process protocol and camera calibration workflow are in [mapping.md](mapping.md). The minimap is a relative trajectory, not a measured floor plan. Automatic movement is gated by an explicit deployment flag, saved-map relocalization, fresh visual risk output, live video and operator supervision. The runner can only request short pulses through the control coordinator; it cannot issue motor HTTP calls. Pi and rover acceptance remains mandatory before enabling the flag.
