# Raspberry Pi 5 deployment and acceptance

## Address and video recovery

Use `ROVER_ADDR=cam-rover.local` or reserve the rover's IPv4 address in the router.
Static Go builds cannot directly use Linux NSS mDNS; the hub resolves `.local`
through bounded `getent ahostsv4` calls. Install/enable `avahi-daemon` and
`libnss-mdns` if `getent ahostsv4 cam-rover.local` does not resolve on the Pi.

Authenticated settings routes are `GET/PUT /api/link` and
`POST /api/link/reconnect`. Only private IPv4 or `.local` targets are accepted.
The target is stored in SQLite (not credentials) and overrides `ROVER_ADDR` on
restart. Saving works even when the robot is off, and keeps motion stopped.
Address changes invalidate the old video stream; a five-second no-frame watchdog
reopens stalled streams. Recovery never resumes motion. Manual driving also
requires fresh video. Use the hub as the only camera upstream client.

Manual activation uses one WebSocket `activate` command: confirm stop, apply the
requested speed, then clear the stop latch. ACK/error replies echo `request_id`
so delayed heartbeats cannot complete another request. Refresh the mobile page
after upgrading. Controls have a 500 ms deadline, still below the ESP32's 700 ms
deadman; movement is never automatically retried after failure. Successful mDNS
lookups are cached for two seconds and invalidated on a failed dial or address
change. This reduces lookup overhead; it does not repair radio loss or power
instability. A control/video fault still requires explicit manual activation.

Normal Pi-to-rover traffic can use the same 2.4 GHz LAN. The separate recovery
AP operation below requires Ethernet/another uplink: switching the Pi's only
Wi-Fi interface to the rover AP will interrupt its Tailscale connection.

This service assumes Ethernet from Pi to the home router; `wlan0` is reserved
for one-time rover AP setup/recovery. Normal Pi-to-rover traffic uses the
ESP32's home Wi-Fi address. Do not expose the ESP32 or Go port with router
port-forwarding. Use **Tailscale Serve**, not Funnel.

## Prepare the Pi

1. Install Go 1.25+, NetworkManager, Tailscale, CMake, OpenCV development
   libraries and stella_vslam. Build the Go service with `go build -o
   cam-rover-hub .` and the worker with `cmake -S vision -B vision/build &&
   cmake --build vision/build -j2`. Follow [mapping.md](mapping.md) to calibrate
   the mounted camera and provide the matching vocabulary.
2. Create an unprivileged `camrover` service user. Place this checkout and its
   built binaries under `/opt/cam-rover-hub`, readable by that user. Put the
   calibrated camera YAML under `/etc/cam-rover-hub/camera.yaml`.
3. Copy `deploy/cam-rover-hub.env.example` to `/etc/cam-rover-hub.env`, set the
   real `ROVER_API_TOKEN` and `TAILSCALE_ALLOWED_LOGIN`, and restrict it to root
   (`chmod 0600`). Compile the ESP32 with the same token and a non-default AP
   password. Keep `AUTO_ENABLED=0` until the hardware checklist passes.
4. On the Pi, create the rover AP profile interactively while connected by
   Ethernet: `sudo nmcli --ask device wifi connect cam-rover ifname wlan0 name
   cam-rover`. Enter the ESP32 AP password at the prompt; do not put it in shell
   arguments. Then run `sudo nmcli connection modify cam-rover
   connection.autoconnect no`. The AP secret is in NetworkManager's protected
   profile, not the hub SQLite database or logs. Test profile activation as
   `camrover`. If policy blocks activation, review the optional
   `deploy/49-cam-rover-hub.rules.example` and install it under
   `/etc/polkit-1/rules.d/` only after accepting that it grants this user
   NetworkManager connection-control rights.
5. Install `deploy/cam-rover-hub.service` in `/etc/systemd/system/`, run
   `sudo systemctl daemon-reload` and `sudo systemctl enable --now
   cam-rover-hub`. Check `systemctl status cam-rover-hub` and
   `curl http://127.0.0.1:8080/healthz`. The process refuses non-loopback
   `LISTEN_ADDR`; only healthz is exempt from identity authorization.

## Private remote access

Join Pi and mobile to the same tailnet, enable tailnet HTTPS certificates,
then on Pi run `sudo tailscale serve --bg 8080`. Check `tailscale serve status`
for the assigned `https://<pi>.<tailnet>.ts.net` URL. Do not run `tailscale
funnel`; Funnel is public and does not forward identity headers. Serve adds
`Tailscale-User-Login`, which the hub compares to `TAILSCALE_ALLOWED_LOGIN`.
The backend must remain bound to `127.0.0.1`; local processes on the Pi remain
inside the trust boundary.

In the tailnet policy, grant only the operator access to the Pi's Serve HTTPS
port. For example, after assigning the Pi `tag:cam-rover`, merge this grant
into the existing policy (replace the example account):

```json
{
  "tagOwners": {"tag:cam-rover": ["owner@example.com"]},
  "grants": [
    {"src": ["owner@example.com"], "dst": ["tag:cam-rover"], "ip": ["tcp:443"]}
  ]
}
```

Check for broader existing ACLs/grants: adding a narrow grant does not revoke
an existing broad allow. Tailscale's official [Serve documentation](https://tailscale.com/docs/features/tailscale-serve), [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve) and [Grants reference](https://tailscale.com/docs/reference/syntax/grants) are the sources for these commands and headers.

## Acceptance before moving wheels

1. With wheels raised, open the Serve URL on mobile. Check `/ws` control,
   `GET /video.mjpeg` from two viewers, Wi-Fi scan, stored/connected status,
   AP-to-home Wi-Fi transition and back. Confirm credentials never appear in
   SQLite or service logs. The local ESP32 recovery page must remain usable.
2. With wheels still raised, drive then close the browser, disconnect mobile
   Tailscale, block Pi-to-ESP32 traffic, interrupt camera stream, kill the
   vision worker and stop the Go service separately. Each must stop motors;
   the ESP32's independent 700 ms deadman is the final fallback. Verify no
   uncommanded restart after restoring links.
3. Replay recorded home video through the worker using `vision/replay.py` and
   inspect pose continuity, `lost` events, visual-risk false negatives and Pi 5
   CPU/latency. Save/reload a map and check relocalization failure prevents
   automatic motion. A minimap path is not a measured floor plan.

   ```sh
   mkdir -p frames
   ffmpeg -i recording.mp4 -vf fps=5 frames/%06d.jpg
   python3 vision/replay.py frames/ replay-check.msg --config /etc/cam-rover-hub/camera.yaml --vocab /opt/cam-rover-hub/orb_vocab.fbow
   ```
4. Only after those checks, set `AUTO_ENABLED=1`, restart the service, and
   repeat with wheels raised. Check short 150 ms minimum-speed pulses, stop
   between pulses, and stop on obstacle-risk/low texture or missing new visual
   assessment. Then make a slow, supervised trial in a clear enclosed indoor
   area, with a human ready to press stop. Optical flow cannot prove free
   space and automatic mode does not steer around obstacles.

No Pi 5, ESP32 flash or physical rover is available in the development
environment. Those checks must be recorded on the hardware before declaring
mapping or automatic exploration production-ready or merging hardware-gated PRs.
