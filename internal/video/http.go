package video

import (
	"fmt"
	"net/http"
	"time"
)

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=hubframe")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	var previous uint64
	lastSent := time.Now()
	for {
		frame, seq, seen, notify := b.Snapshot()
		if seq == previous || seen.IsZero() || time.Since(seen) >= 2*time.Second {
			// Retry notifications must not reset the no-frame budget indefinitely.
			remaining := 5*time.Second - time.Since(lastSent)
			if remaining <= 0 {
				return
			}
			idle := time.NewTimer(remaining)
			select {
			case <-r.Context().Done():
				idle.Stop()
				return
			case <-notify:
				idle.Stop()
				continue
			case <-idle.C:
				return
			}
		}
		// Slow viewers may never block the shared capture/stop path indefinitely.
		if err := controller.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil && err != http.ErrNotSupported {
			return
		}
		if _, err := fmt.Fprintf(w, "--hubframe\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame)); err != nil {
			return
		}
		if _, err := w.Write(frame); err != nil {
			return
		}
		if _, err := w.Write([]byte("\r\n")); err != nil {
			return
		}
		if err := controller.Flush(); err != nil && err != http.ErrNotSupported {
			return
		}
		previous = seq
		lastSent = time.Now()
	}
}
