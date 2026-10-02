package vision

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/store"
)

const maxFrame = 1 << 20

type Frames interface {
	Snapshot() ([]byte, uint64, time.Time, <-chan struct{})
}

type Config struct {
	Worker     string
	Camera     string
	Vocabulary string
	MapDir     string
}

type Status struct {
	MapID         int64       `json:"map_id"`
	Running       bool        `json:"running"`
	Loaded        bool        `json:"loaded"`
	Tracking      bool        `json:"tracking"`
	LastPose      *store.Pose `json:"last_pose,omitempty"`
	RiskSafe      bool        `json:"risk_safe"`
	RiskFresh     bool        `json:"risk_fresh"`
	RiskTracks    int         `json:"risk_tracks"`
	RiskExpansion float64     `json:"risk_expansion"`
	RiskSeq       uint64      `json:"risk_seq"`
	Error         string      `json:"error,omitempty"`
}

type Manager struct {
	mu           sync.Mutex
	db           *sql.DB
	frames       Frames
	cfg          Config
	job          *job
	status       Status
	lastPoseTime time.Time
	lastRiskTime time.Time
}

type job struct {
	id      int64
	path    string
	loaded  bool
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stop    chan struct{}
	done    chan struct{}
	parsed  chan struct{}
	once    sync.Once
	tracked bool // guarded by Manager.mu
	saving  bool // guarded by Manager.mu
}

type workerMessage struct {
	Type       string  `json:"type"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Heading    float64 `json:"heading"`
	Confidence float64 `json:"confidence"`
	Safe       bool    `json:"safe"`
	Tracks     int     `json:"tracks"`
	Expansion  float64 `json:"expansion"`
}

func New(db *sql.DB, frames Frames, cfg Config) *Manager {
	return &Manager{db: db, frames: frames, cfg: cfg}
}

func (m *Manager) DB() *sql.DB { return m.db }

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	if s.Tracking && m.job != nil && time.Since(m.lastPoseTime) > time.Second {
		s.Tracking = false
	}
	s.RiskFresh = m.job != nil && !m.lastRiskTime.IsZero() && time.Since(m.lastRiskTime) < time.Second
	if !s.RiskFresh {
		s.RiskSafe = false
	}
	return s
}

// lastPoseTime is intentionally independent from the persisted pose timestamp.
func (m *Manager) Ready() bool { return m.Status().Tracking }

func (m *Manager) ReadyForAuto() bool {
	s := m.Status()
	return s.Loaded && s.Running && s.Tracking && s.RiskFresh && s.RiskSafe
}

func (m *Manager) StartNew(ctx context.Context, name string) (store.Map, error) {
	if name == "" {
		name = "새 지도"
	}
	if err := m.validate(); err != nil {
		return store.Map{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job != nil {
		return store.Map{}, errors.New("map job already active")
	}
	mapRecord, err := store.CreateMap(ctx, m.db, name)
	if err != nil {
		return store.Map{}, err
	}
	path := filepath.Join(m.cfg.MapDir, fmt.Sprintf("map-%d.msg", mapRecord.ID))
	if err := m.start(mapRecord.ID, path, false); err != nil {
		_ = store.SetMapStatus(ctx, m.db, mapRecord.ID, "failed", "")
		return store.Map{}, err
	}
	return mapRecord, nil
}

func (m *Manager) Load(ctx context.Context, id int64) error {
	if err := m.validate(); err != nil {
		return err
	}
	mapRecord, err := store.GetMap(ctx, m.db, id)
	if err != nil {
		return err
	}
	if mapRecord.Status != "saved" || mapRecord.FilePath == "" {
		return errors.New("map is not saved")
	}
	if _, err := os.Stat(mapRecord.FilePath); err != nil {
		return fmt.Errorf("map file unavailable: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job != nil {
		return errors.New("map job already active")
	}
	return m.start(id, mapRecord.FilePath, true)
}

func (m *Manager) validate() error {
	for _, path := range []string{m.cfg.Worker, m.cfg.Camera, m.cfg.Vocabulary} {
		if path == "" {
			return errors.New("vision worker, calibration and vocabulary are required")
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("vision dependency unavailable: %w", err)
		}
	}
	return os.MkdirAll(m.cfg.MapDir, 0700)
}

func (m *Manager) start(id int64, path string, loaded bool) error {
	args := []string{"--config", m.cfg.Camera, "--vocab", m.cfg.Vocabulary, "--map", path}
	if loaded {
		args = append(args, "--load")
	}
	cmd := exec.Command(m.cfg.Worker, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	j := &job{id: id, path: path, loaded: loaded, cmd: cmd, stdin: stdin, stop: make(chan struct{}), done: make(chan struct{}), parsed: make(chan struct{})}
	m.job = j
	m.status = Status{MapID: id, Running: true, Loaded: loaded}
	m.lastPoseTime = time.Time{}
	m.lastRiskTime = time.Time{}
	go m.consume(j, stdout)
	go m.feed(j)
	go m.wait(j)
	return nil
}

func (m *Manager) consume(j *job, stdout io.Reader) {
	defer close(j.parsed)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		var event workerMessage
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		m.mu.Lock()
		if m.job == j {
			switch event.Type {
			case "pose":
				if validPose(event) {
					p := store.Pose{X: event.X, Y: event.Y, Heading: event.Heading, Confidence: event.Confidence}
					m.status.LastPose = &p
					m.status.Tracking = true
					m.lastPoseTime = time.Now()
					j.tracked = true
					m.mu.Unlock()
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					_ = store.AddPose(ctx, m.db, j.id, p)
					cancel()
					continue
				}
			case "lost":
				m.status.Tracking = false
				m.status.RiskSafe = false
			case "risk":
				if isFinite(event.Expansion) && event.Expansion >= 0 && event.Tracks >= 0 && event.Tracks <= 1000 {
					m.status.RiskSafe = event.Safe && event.Tracks >= 30 && event.Expansion < 0.015
					m.status.RiskTracks = event.Tracks
					m.status.RiskExpansion = event.Expansion
					m.status.RiskSeq++
					m.lastRiskTime = time.Now()
				}
			}
		}
		m.mu.Unlock()
	}
	if err := scanner.Err(); err != nil {
		m.mu.Lock()
		if m.job == j {
			m.status.Error = "vision output failed"
		}
		m.mu.Unlock()
		_ = j.cmd.Process.Kill()
	}
}

func (m *Manager) feed(j *job) {
	defer j.stdin.Close()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last uint64
	for {
		select {
		case <-j.stop:
			return
		case <-ticker.C:
		}
		frame, seq, seen, _ := m.frames.Snapshot()
		if seq == last || time.Since(seen) > time.Second || len(frame) == 0 {
			continue
		}
		last = seq
		if err := writeFrame(j.stdin, frame, seen); err != nil {
			j.cmd.Process.Kill()
			return
		}
	}
}

func writeFrame(w io.Writer, frame []byte, seen time.Time) error {
	if len(frame) > maxFrame {
		return errors.New("frame too large")
	}
	var header [12]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(frame)))
	binary.BigEndian.PutUint64(header[4:], uint64(seen.UnixNano()))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, frame)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func validPose(p workerMessage) bool {
	return isFinite(p.X) && isFinite(p.Y) && isFinite(p.Heading) && isFinite(p.Confidence) && p.Confidence >= 0 && p.Confidence <= 1
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (m *Manager) wait(j *job) {
	<-j.parsed
	err := j.cmd.Wait()
	j.once.Do(func() { close(j.stop) })
	m.mu.Lock()
	if m.job == j {
		m.status.Running = false
		m.status.Tracking = false
		if err != nil {
			m.status.Error = "vision worker exited: " + err.Error()
		}
		m.job = nil
	}
	m.mu.Unlock()
	if !j.loaded {
		status, path := "failed", ""
		m.mu.Lock()
		canSave := j.saving && j.tracked
		m.mu.Unlock()
		if err == nil && canSave {
			if info, statErr := os.Stat(j.path); statErr == nil && info.Size() > 0 {
				status, path = "saved", j.path
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = store.SetMapStatus(ctx, m.db, j.id, status, path)
		cancel()
	}
	close(j.done)
}

func (m *Manager) StopAndSave(ctx context.Context) error {
	m.mu.Lock()
	j := m.job
	if j != nil {
		j.saving = true
	}
	m.mu.Unlock()
	if j == nil {
		return errors.New("no active map job")
	}
	j.once.Do(func() { close(j.stop) })
	select {
	case <-j.done:
	case <-ctx.Done():
		_ = j.cmd.Process.Kill()
		<-j.done
		return ctx.Err()
	}
	if j.loaded {
		return nil
	}
	record, err := store.GetMap(ctx, m.db, j.id)
	if err != nil {
		return err
	}
	if record.Status != "saved" {
		return errors.New("vision worker could not save map")
	}
	return nil
}

func (m *Manager) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = m.StopAndSave(ctx)
}
