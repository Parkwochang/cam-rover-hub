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
	source Source
	mu     sync.RWMutex
	frame  []byte
	seq    uint64
	seen   time.Time
	notify chan struct{}
}

func New(source Source) *Broker { return &Broker{source: source, notify: make(chan struct{})} }

func (b *Broker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		resp, err := b.source.OpenStream(ctx)
		if err == nil {
			err = b.read(ctx, resp)
			resp.Body.Close()
		}
		_ = err
		b.mu.Lock()
		b.seen = time.Time{}
		close(b.notify)
		b.notify = make(chan struct{})
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
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
