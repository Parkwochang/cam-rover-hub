package vision

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/store"
)

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(b []byte) (int, error) {
	if len(b) > 3 {
		b = b[:3]
	}
	return w.Buffer.Write(b)
}

type deadWriter struct{}

func (deadWriter) Write([]byte) (int, error) { return 0, nil }

func TestWriteFrameProtocol(t *testing.T) {
	var output shortWriter
	stamp := time.Unix(123, 456)
	if err := writeFrame(&output, []byte{1, 2, 3, 4}, stamp); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	if len(data) != 16 || binary.BigEndian.Uint32(data[:4]) != 4 || binary.BigEndian.Uint64(data[4:12]) != uint64(stamp.UnixNano()) || !bytes.Equal(data[12:], []byte{1, 2, 3, 4}) {
		t.Fatalf("unexpected protocol bytes: %v", data)
	}
	if !errors.Is(writeFrame(deadWriter{}, []byte{1}, stamp), io.ErrShortWrite) {
		t.Fatal("expected short-write error")
	}
	if writeFrame(io.Discard, make([]byte, maxFrame+1), stamp) == nil {
		t.Fatal("oversized frame accepted")
	}
}

func TestInvalidPoseRejected(t *testing.T) {
	if !validPose(workerMessage{Type: "pose", X: 1, Confidence: 1}) {
		t.Fatal("valid pose rejected")
	}
	if validPose(workerMessage{Type: "pose", X: math.NaN(), Confidence: 1}) {
		t.Fatal("NaN accepted")
	}
	if validPose(workerMessage{Type: "pose", X: 1, Confidence: 2}) {
		t.Fatal("invalid confidence accepted")
	}
}

type fakeFrames struct{}

func (fakeFrames) Snapshot() ([]byte, uint64, time.Time, <-chan struct{}) {
	return []byte{0xff, 0xd8, 0xff, 0xd9}, 1, time.Now(), nil
}

func TestWorkerLifecycleAndMapReload(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker.sh")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    --map) map="$2"; shift 2 ;;
    --load) loaded=1; shift ;;
    *) shift ;;
  esac
done
printf '{"type":"pose","x":1.5,"y":2.5,"heading":0.2,"confidence":1}\n'
cat >/dev/null
if [ -z "$loaded" ]; then printf 'map' > "$map"; fi
`
	if err := os.WriteFile(worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"camera.yaml", "vocab.fbow"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(filepath.Join(dir, "rover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := New(db, fakeFrames{}, Config{Worker: worker, Camera: filepath.Join(dir, "camera.yaml"), Vocabulary: filepath.Join(dir, "vocab.fbow"), MapDir: filepath.Join(dir, "maps")})
	record, err := m.StartNew(t.Context(), "test map")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !m.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !m.Ready() {
		t.Fatal("worker pose not received")
	}
	if err := m.StopAndSave(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetMap(t.Context(), db, record.ID)
	if err != nil || saved.Status != "saved" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	poses, err := store.ListPoses(t.Context(), db, record.ID)
	if err != nil || len(poses) != 1 {
		t.Fatalf("poses = %+v, %v", poses, err)
	}
	if err := m.Load(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for !m.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !m.Ready() {
		t.Fatal("saved map did not relocalize")
	}
	if err := m.StopAndSave(t.Context()); err != nil {
		t.Fatal(err)
	}
}
