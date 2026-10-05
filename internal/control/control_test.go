package control

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMotor struct {
	mu         sync.Mutex
	directions []string
}

func (f *fakeMotor) Move(_ context.Context, direction string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directions = append(f.directions, direction)
	return nil
}
func (f *fakeMotor) Speed(context.Context, int) error { return nil }
func (f *fakeMotor) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.directions[len(f.directions)-1]
}

func TestOnlyOneOwnerAndDisconnectStops(t *testing.T) {
	motor := &fakeMotor{}
	c := New(motor)
	defer c.Close()
	if err := c.Drive(1, "forward"); err != nil {
		t.Fatal(err)
	}
	if err := c.Drive(2, "left"); err != ErrBusy {
		t.Fatalf("competing owner: %v", err)
	}
	c.StopOwner(1)
	if motor.last() != "stop" {
		t.Fatalf("last command = %q", motor.last())
	}
}

func TestHeartbeatExpiryStops(t *testing.T) {
	motor := &fakeMotor{}
	c := New(motor)
	defer c.Close()
	if err := c.Drive(1, "forward"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(900 * time.Millisecond)
	for motor.last() != "stop" {
		select {
		case <-deadline:
			t.Fatal("rover did not stop")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestManualDriveRequiresVideoAndStopsWhenVideoLost(t *testing.T) {
	motor := &fakeMotor{}
	c := New(motor)
	defer c.Close()
	var healthy atomic.Bool
	c.SetGuards(healthy.Load, func() bool { return false })
	if err := c.Drive(1, "forward"); err == nil {
		t.Fatal("blind drive allowed")
	}
	healthy.Store(true)
	if err := c.Drive(1, "forward"); err != nil {
		t.Fatal(err)
	}
	healthy.Store(false)
	deadline := time.Now().Add(500 * time.Millisecond)
	for c.Status().Direction != "stop" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Status().Direction != "stop" {
		t.Fatal("video loss did not stop manual drive")
	}
	healthy.Store(true)
	time.Sleep(120 * time.Millisecond)
	if c.Status().Direction != "stop" {
		t.Fatal("video recovery resumed motion")
	}
	if err := c.Drive(1, "forward"); err == nil {
		t.Fatal("old input cleared the fault latch")
	}
	if err := c.Supervise(1); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(1, "manual"); err != nil {
		t.Fatal(err)
	}
	if err := c.Drive(1, "forward"); err != nil {
		t.Fatal(err)
	}
}

func TestReconfigureRejectsConcurrentDriveAndRemainsStopped(t *testing.T) {
	motor := &fakeMotor{}
	c := New(motor)
	defer c.Close()
	if err := c.Drive(1, "forward"); err != nil {
		t.Fatal(err)
	}
	err := c.Reconfigure(func() error {
		if err := c.Drive(1, "left"); err != ErrBusy {
			t.Fatalf("drive during reconfigure: %v", err)
		}
		return nil
	})
	if err != nil || c.Status().Direction != "stop" || motor.last() != "stop" {
		t.Fatal("unsafe reconfiguration")
	}
}

func TestAutoModeRequiresHealthySignalsAndStopsOnVideoLoss(t *testing.T) {
	motor := &fakeMotor{}
	c := New(motor)
	defer c.Close()
	var videoOK atomic.Bool
	c.SetGuards(videoOK.Load, func() bool { return true })
	if err := c.Supervise(1); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(1, "auto"); err != ErrAutoUnavailable {
		t.Fatalf("unhealthy video: %v", err)
	}
	videoOK.Store(true)
	if err := c.Supervise(1); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(1, "auto"); err != nil {
		t.Fatal(err)
	}
	if err := c.AutoDrive("forward"); err != nil {
		t.Fatal(err)
	}
	videoOK.Store(false)
	deadline := time.After(500 * time.Millisecond)
	for c.Status().Mode != "manual" {
		select {
		case <-deadline:
			t.Fatal("auto mode did not disarm")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	for motor.last() != "stop" {
		select {
		case <-deadline:
			t.Fatalf("last command = %q", motor.last())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
