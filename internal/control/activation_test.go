package control

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type activationMotor struct {
	mu                    sync.Mutex
	commands              []string
	entered, release      chan struct{}
	stopError, speedError error
}

func (m *activationMotor) Move(ctx context.Context, direction string) error {
	m.mu.Lock()
	m.commands = append(m.commands, direction)
	m.mu.Unlock()
	if m.entered != nil {
		select {
		case m.entered <- struct{}{}:
		default:
		}
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.stopError
}
func (m *activationMotor) Speed(context.Context, int) error {
	m.mu.Lock()
	m.commands = append(m.commands, "speed")
	m.mu.Unlock()
	return m.speedError
}

func TestActivationAcceptsSameOwnerHeartbeatWithoutCancelling(t *testing.T) {
	m := &activationMotor{entered: make(chan struct{}, 1), release: make(chan struct{})}
	c := New(m)
	defer c.Close()
	c.SetGuards(func() bool { return true }, nil)
	c.mu.Lock()
	c.fault = "stop latch"
	c.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- c.ActivateManual(1, 85) }()
	<-m.entered
	if err := c.Supervise(1); err != nil {
		t.Fatalf("own heartbeat cancelled activation: %v", err)
	}
	if err := c.Supervise(2); err != ErrBusy {
		t.Fatalf("other owner admitted: %v", err)
	}
	// A 400 ms stop is allowed, without relaxing the independent 700 ms stop.
	time.Sleep(400 * time.Millisecond)
	close(m.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); status.Fault != "" || status.Direction != "stop" {
		t.Fatalf("status=%+v", status)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commands) != 2 || m.commands[0] != "stop" || m.commands[1] != "speed" {
		t.Fatalf("commands=%v", m.commands)
	}
}

func TestDisconnectDuringActivationNeverRearms(t *testing.T) {
	m := &activationMotor{entered: make(chan struct{}, 1), release: make(chan struct{})}
	c := New(m)
	defer c.Close()
	done := make(chan error, 1)
	go func() { done <- c.ActivateManual(1, 85) }()
	<-m.entered
	stopped := make(chan struct{})
	go func() { c.StopOwner(1); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		cancelled := c.transitionCancelled
		c.mu.Unlock()
		if cancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnect did not cancel")
		}
		time.Sleep(time.Millisecond)
	}
	close(m.release)
	if err := <-done; err != ErrBusy {
		t.Fatalf("activation after disconnect: %v", err)
	}
	<-stopped
	if c.Status().Direction != "stop" || c.Status().Fault == "" {
		t.Fatal("disconnect must latch stop")
	}
	for _, command := range m.commands {
		if command == "speed" {
			t.Fatal("speed sent after cancellation")
		}
	}
}

func TestActivationMustConfirmStopAndSpeed(t *testing.T) {
	for _, failed := range []string{"stop", "speed"} {
		t.Run(failed, func(t *testing.T) {
			m := &activationMotor{}
			if failed == "stop" {
				m.stopError = errors.New("offline")
			} else {
				m.speedError = errors.New("offline")
			}
			c := New(m)
			defer c.Close()
			if err := c.ActivateManual(1, 85); err == nil {
				t.Fatal("failed request activated rover")
			}
			if c.Status().Fault == "" || c.Status().Direction != "stop" {
				t.Fatal("fault not latched")
			}
			if failed == "stop" {
				for _, command := range m.commands {
					if command == "speed" {
						t.Fatal("speed sent without confirmed stop")
					}
				}
			}
		})
	}
}

func TestExpiredQueuedMovementIsNotSent(t *testing.T) {
	m := &activationMotor{}
	c := New(m)
	defer c.Close()
	c.mu.Lock()
	c.direction = "forward"
	c.lastInput = time.Now().Add(-600 * time.Millisecond)
	c.mu.Unlock()
	if err := c.send("forward"); err == nil {
		t.Fatal("expired movement accepted")
	}
	if len(m.commands) != 0 {
		t.Fatal("expired movement reached motor")
	}
}

func TestVideoLostDuringActivationKeepsFaultLatched(t *testing.T) {
	m := &activationMotor{}
	c := New(m)
	defer c.Close()
	calls := 0
	c.SetGuards(func() bool { calls++; return calls == 1 }, nil)
	if err := c.ActivateManual(1, 85); err == nil {
		t.Fatal("activation completed after video loss")
	}
	if c.Status().Fault == "" || c.Status().Direction != "stop" {
		t.Fatal("video loss did not retain stop latch")
	}
}
