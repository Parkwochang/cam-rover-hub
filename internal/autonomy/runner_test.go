package autonomy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/vision"
)

type fakeController struct {
	mu       sync.Mutex
	mode     string
	commands []string
	fault    string
}

func (f *fakeController) Status() control.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return control.Status{Mode: f.mode}
}
func (f *fakeController) AutoDrive(direction string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, direction)
	return nil
}
func (f *fakeController) FailAuto(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = "manual"
	f.fault = reason
	f.commands = append(f.commands, "stop")
}
func (f *fakeController) snapshot() ([]string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...), f.fault
}

type fakeVision struct {
	ready bool
	seq   uint64
}

func (f *fakeVision) ReadyForAuto() bool    { return f.ready }
func (f *fakeVision) Status() vision.Status { return vision.Status{RiskSeq: f.seq} }

func TestUncertainVisionNeverDrives(t *testing.T) {
	c := &fakeController{mode: "auto"}
	cycle(context.Background(), c, &fakeVision{}, 10*time.Millisecond, 50*time.Millisecond)
	commands, fault := c.snapshot()
	if len(commands) != 1 || commands[0] != "stop" || fault == "" {
		t.Fatalf("commands=%v fault=%q", commands, fault)
	}
}

func TestPulseStopsThenMissingRecheckDisarms(t *testing.T) {
	c := &fakeController{mode: "auto"}
	cycle(context.Background(), c, &fakeVision{ready: true, seq: 1}, 10*time.Millisecond, 50*time.Millisecond)
	commands, fault := c.snapshot()
	if len(commands) != 3 || commands[0] != "forward" || commands[1] != "stop" || commands[2] != "stop" || fault != "visual recheck timed out" {
		t.Fatalf("commands=%v fault=%q", commands, fault)
	}
}
