package control

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Motor interface {
	Move(context.Context, string) error
	Speed(context.Context, int) error
}

var ErrBusy = errors.New("another operator controls the rover")
var ErrDirection = errors.New("invalid direction")

type Coordinator struct {
	mu        sync.Mutex
	sendMu    sync.Mutex
	motor     Motor
	owner     uint64
	direction string
	lastInput time.Time
	lastSend  time.Time
	mode      string
	cancel    context.CancelFunc
}

func New(motor Motor) *Coordinator {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Coordinator{motor: motor, direction: "stop", mode: "manual", cancel: cancel}
	go c.watch(ctx)
	return c
}

func (c *Coordinator) Close() {
	c.cancel()
	c.Stop()
}

func validDirection(direction string) bool {
	switch direction {
	case "stop", "forward", "backward", "left", "right", "forward-left", "forward-right", "backward-left", "backward-right":
		return true
	}
	return false
}

func (c *Coordinator) Drive(owner uint64, direction string) error {
	if !validDirection(direction) {
		return ErrDirection
	}
	if direction == "stop" {
		c.Stop()
		return nil
	}
	c.mu.Lock()
	if c.mode != "manual" || (c.owner != 0 && c.owner != owner) {
		c.mu.Unlock()
		return ErrBusy
	}
	c.owner = owner
	c.direction = direction
	c.lastInput = time.Now()
	c.mu.Unlock()
	if err := c.send(direction); err != nil {
		c.Stop()
		return err
	}
	return nil
}

func (c *Coordinator) Speed(owner uint64, speed int) error {
	if speed < 85 || speed > 255 {
		return errors.New("speed must be 85-255")
	}
	c.mu.Lock()
	allowed := c.mode == "manual" && (c.owner == 0 || c.owner == owner)
	c.mu.Unlock()
	if !allowed {
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	return c.motor.Speed(ctx, speed)
}

func (c *Coordinator) StopOwner(owner uint64) {
	c.mu.Lock()
	owned := c.owner == owner
	c.mu.Unlock()
	if owned {
		c.Stop()
	}
}

func (c *Coordinator) Stop() {
	c.mu.Lock()
	c.owner = 0
	c.direction = "stop"
	c.lastInput = time.Time{}
	c.mu.Unlock()
	_ = c.send("stop")
}

func (c *Coordinator) Status() (mode, direction string, occupied bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode, c.direction, c.owner != 0
}

func (c *Coordinator) send(direction string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	if direction != "stop" && c.direction != direction {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	err := c.motor.Move(ctx, direction)
	c.mu.Lock()
	c.lastSend = time.Now()
	c.mu.Unlock()
	return err
}

func (c *Coordinator) watch(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			stale := c.direction != "stop" && time.Since(c.lastInput) > 550*time.Millisecond
			repeat := c.direction != "stop" && time.Since(c.lastSend) > 250*time.Millisecond
			direction := c.direction
			c.mu.Unlock()
			if stale {
				c.Stop()
			} else if repeat {
				_ = c.send(direction)
			}
		}
	}
}
