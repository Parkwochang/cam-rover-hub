# Cam Rover Hub rules

- The control coordinator is the only package allowed to issue motor commands to the rover.
- HTTP handlers and the vision worker may request motion only through the coordinator.
- Never persist or log Wi-Fi passwords, API tokens, or video frames in SQLite.
- Every network request and worker operation needs a timeout or cancellation path. Bound frame queues and drop old frames for slow consumers.
- Stop the rover on lost operator heartbeat, lost video, lost tracking, or mode transition. Never resume motion automatically after a fault.
- Treat monocular SLAM coordinates as scale-free relative estimates, never as measured distances or a verified obstacle map. Do not enable automatic motion before Pi/rover acceptance tests.
- Keep the Go service bound to localhost when using Tailscale Serve and verify the caller identity at the edge.
- Before a PR run `go test ./...`, `go vet ./...`, and the architecture dependency check. Record Pi and rover hardware checks separately.
