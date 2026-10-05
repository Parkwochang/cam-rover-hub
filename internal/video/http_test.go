package video

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestFailureNotificationsDoNotExtendViewerBudget(t *testing.T) {
	broker := New(nil)
	done := make(chan struct{})
	go func() {
		broker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/video.mjpeg", nil))
		close(done)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(6 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("offline viewer stayed open despite no frames")
		case <-ticker.C:
			broker.mu.Lock()
			close(broker.notify)
			broker.notify = make(chan struct{})
			broker.mu.Unlock()
		}
	}
}
