package video

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"sync/atomic"
	"testing"
	"time"
)

type pipeSource struct {
	count    atomic.Int32
	reader   *io.PipeReader
	boundary string
}

func (p *pipeSource) OpenStream(ctx context.Context) (*http.Response, error) {
	p.count.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"multipart/x-mixed-replace; boundary=" + p.boundary}}, Body: p.reader}, nil
}

func TestOneUpstreamFeedsMultipleClients(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	multi := multipart.NewWriter(writer)
	source := &pipeSource{reader: reader, boundary: multi.Boundary()}
	broker := New(source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go broker.Run(ctx)
	go func() {
		part, err := multi.CreatePart(textproto.MIMEHeader{"Content-Type": []string{"image/jpeg"}})
		if err == nil {
			_, _ = part.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
			_, _ = multi.CreatePart(textproto.MIMEHeader{"Content-Type": []string{"image/jpeg"}})
		}
	}()
	deadline := time.After(time.Second)
	for !broker.Healthy() {
		select {
		case <-deadline:
			t.Fatal("no frame received")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	for i := 0; i < 2; i++ {
		client := &http.Client{Timeout: time.Second}
		resp, err := client.Get(server.URL + "/video.mjpeg")
		if err != nil {
			t.Fatal(err)
		}
		part, err := multipart.NewReader(resp.Body, "hubframe").NextPart()
		if err != nil {
			t.Fatal(err)
		}
		frame := make([]byte, 4)
		if _, err := io.ReadFull(part, frame); err != nil {
			t.Fatal(err)
		}
		if frame[0] != 0xff || frame[1] != 0xd8 || frame[2] != 0xff || frame[3] != 0xd9 {
			t.Fatalf("bad JPEG: %x", frame)
		}
		resp.Body.Close()
	}
	if source.count.Load() != 1 {
		t.Fatalf("upstream connections = %d", source.count.Load())
	}
}
