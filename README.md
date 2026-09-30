# cam-rover-hub

Raspberry Pi hub for the ESP32-CAM rover. This repository currently contains a minimal Gin server; rover control and video streaming will be added later.

## Requirements

- Go 1.25 or newer

## Run

```sh
go run .
```

The server listens on port `8080` by default. Set `PORT` to use another port.

```sh
curl http://localhost:8080/healthz
```

Expected response: `{"status":"ok"}`.
