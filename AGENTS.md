# Cam Rover Hub rules

- The control coordinator is the only package allowed to issue motor commands to the rover.
- HTTP handlers and the vision worker may request motion only through the coordinator.
- Never persist or log Wi-Fi passwords, API tokens, or video frames in SQLite.
- Every network request and worker operation needs a timeout or cancellation path. Bound frame queues and drop old frames for slow consumers.
- Stop the rover on lost operator heartbeat, lost video, lost tracking, or mode transition. Never resume motion automatically after a fault.
- Treat monocular SLAM coordinates as scale-free relative estimates, never as measured distances or a verified obstacle map. Do not enable automatic motion before Pi/rover acceptance tests.
- Keep `AUTO_ENABLED` off by default. Visual optical-flow risk is an experimental stop signal, not proof of clear space; require a focused human supervisor, 85/255 speed and short forward pulses, and never auto-restart after a fault.
- Keep the Go service bound to localhost when using Tailscale Serve and verify the caller identity at the edge.
- Deployment must require a specific `Tailscale-User-Login` for every control, video and map route; never trust that header on a non-loopback listener. Do not use public Tailscale Funnel for rover control.
- Before a PR run `go test ./...`, `go vet ./...`, and the architecture dependency check. Record Pi and rover hardware checks separately.
