package video

import (
	"fmt"
	"net/http"
	"time"
)

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=hubframe")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	var previous uint64
	for {
		frame, seq, seen, notify := b.Snapshot()
		if seq == previous || seen.IsZero() || time.Since(seen) >= 2*time.Second {
			select {
			case <-r.Context().Done():
				return
			case <-notify:
				continue
			}
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
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		previous = seq
	}
}
