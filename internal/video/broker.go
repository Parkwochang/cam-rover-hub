package video

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sync"
	"time"
)

const maxFrame = 1 << 20

type Source interface {
	OpenStream(context.Context) (*http.Response, error)
}

type Broker struct {
	source      Source
	mu          sync.RWMutex
	frame       []byte
	seq         uint64
	seen        time.Time
	notify      chan struct{}
	reconnect   chan struct{}
	streamError string
	idleTimeout time.Duration
}

func New(source Source) *Broker {
	return &Broker{source: source, notify: make(chan struct{}), reconnect: make(chan struct{}, 1), idleTimeout: 5 * time.Second}
}

type Status struct {
	Healthy   bool      `json:"healthy"`
	Frames    uint64    `json:"frames"`
	LastFrame time.Time `json:"last_frame"`
	Error     string    `json:"error,omitempty"`
}

func (b *Broker) Status() Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Status{Healthy: !b.seen.IsZero() && time.Since(b.seen) < 2*time.Second, Frames: b.seq, LastFrame: b.seen, Error: b.streamError}
}

func (b *Broker) Reconnect() {
	select {
	case b.reconnect <- struct{}{}:
	default:
	}
}

func (b *Broker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		var changed <-chan struct{}
		if source, ok := b.source.(interface{ Changed() <-chan struct{} }); ok {
			changed = source.Changed()
		}
		attempt, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			select {
			case <-changed:
				cancel()
			case <-b.reconnect:
				cancel()
			case <-attempt.Done():
			}
			close(done)
		}()
		resp, err := b.source.OpenStream(attempt)
		if err == nil {
			readDone := make(chan struct{})
			go func() {
				select {
				case <-attempt.Done():
					resp.Body.Close()
				case <-readDone:
				}
			}()
			watchdogDone := make(chan struct{})
			go b.watchFrames(attempt, cancel, watchdogDone)
			err = b.read(attempt, resp)
			close(readDone)
			resp.Body.Close()
			cancel()
			<-watchdogDone
		}
		cancel()
		<-done
		b.mu.Lock()
		b.seen = time.Time{}
		b.frame = nil
		if err != nil {
			b.streamError = "camera unavailable; retrying"
		}
		close(b.notify)
		b.notify = make(chan struct{})
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		case <-changed:
		}
	}
}

func (b *Broker) watchFrames(ctx context.Context, cancel context.CancelFunc, done chan struct{}) {
	defer close(done)
	started := time.Now()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _, seen, _ := b.Snapshot()
			if seen.Before(started) {
				seen = started
			}
			if time.Since(seen) > b.idleTimeout {
				cancel()
				return
			}
		}
	}
}

func (b *Broker) read(ctx context.Context, resp *http.Response) error {
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/x-mixed-replace" || params["boundary"] == "" {
		return fmt.Errorf("invalid rover MJPEG content type")
	}
	reader := multipart.NewReader(resp.Body, params["boundary"])
	for ctx.Err() == nil {
		part, err := reader.NextPart()
		if err != nil {
			return err
		}
		if part.Header.Get("Content-Type") != "image/jpeg" {
			part.Close()
			continue
		}
		frame, err := io.ReadAll(io.LimitReader(part, maxFrame+1))
		part.Close()
		if err != nil {
			return err
		}
		if len(frame) < 4 || len(frame) > maxFrame || frame[0] != 0xff || frame[1] != 0xd8 || frame[len(frame)-2] != 0xff || frame[len(frame)-1] != 0xd9 {
			return fmt.Errorf("invalid JPEG frame")
		}
		b.publish(frame)
	}
	return ctx.Err()
}

func (b *Broker) publish(frame []byte) {
	b.mu.Lock()
	b.frame = frame
	b.seq++
	b.seen = time.Now()
	b.streamError = ""
	close(b.notify)
	b.notify = make(chan struct{})
	b.mu.Unlock()
}

func (b *Broker) Snapshot() (frame []byte, seq uint64, seen time.Time, notify <-chan struct{}) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.frame, b.seq, b.seen, b.notify
}

func (b *Broker) Healthy() bool {
	_, _, seen, _ := b.Snapshot()
	return !seen.IsZero() && time.Since(seen) < 2*time.Second
}
