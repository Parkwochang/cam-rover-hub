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
var ErrAutoUnavailable = errors.New("automatic driving is not ready")

type Status struct {
	Mode      string `json:"mode"`
	Direction string `json:"direction"`
	Occupied  bool   `json:"occupied"`
	Fault     string `json:"fault,omitempty"`
}

type Coordinator struct {
	mu                  sync.Mutex
	sendMu              sync.Mutex
	motor               Motor
	owner               uint64
	direction           string
	lastInput           time.Time
	lastSend            time.Time
	mode                string
	supervisor          uint64
	lastSupervisor      time.Time
	videoHealthy        func() bool
	autoReady           func() bool
	fault               string
	transitioning       bool
	transitionCancelled bool
	cancel              context.CancelFunc
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

// Reconfigure prevents new motor commands while changing the rover transport.
// Always stop both the old and new target; reconnecting never resumes motion.
func (c *Coordinator) Reconfigure(change func() error) error {
	c.mu.Lock()
	if c.transitioning {
		c.mu.Unlock()
		return ErrBusy
	}
	c.transitioning = true
	c.transitionCancelled = false
	c.mu.Unlock()
	c.stop(false)
	err := change()
	c.stop(false)
	c.mu.Lock()
	c.transitioning = false
	c.transitionCancelled = false
	c.fault = "rover link changed; activate manual control before driving"
	c.mu.Unlock()
	return err
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
	if c.transitioning || c.mode != "manual" || (c.owner != 0 && c.owner != owner) {
		c.mu.Unlock()
		return ErrBusy
	}
	if c.fault != "" {
		c.mu.Unlock()
		return errors.New("activate manual control to clear the stop latch")
	}
	if c.videoHealthy != nil && !c.videoHealthy() {
		c.mu.Unlock()
		return errors.New("video unavailable; rover remains stopped")
	}
	c.owner = owner
	c.direction = direction
	c.lastInput = time.Now()
	c.mu.Unlock()
	if err := c.send(direction); err != nil {
		c.fail("rover control link lost")
		return err
	}
	return nil
}

func (c *Coordinator) Speed(owner uint64, speed int) error {
	if speed < 85 || speed > 255 {
		return errors.New("speed must be 85-255")
	}
	c.mu.Lock()
	allowed := !c.transitioning && c.mode == "manual" && (c.owner == 0 || c.owner == owner)
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
	owned := c.owner == owner || c.supervisor == owner
	c.mu.Unlock()
	if owned {
		c.fail("operator disconnected")
	}
}

func (c *Coordinator) Stop() {
	c.stop(true)
}

func (c *Coordinator) stop(cancelTransition bool) {
	c.mu.Lock()
	if cancelTransition && c.transitioning {
		c.transitionCancelled = true
	}
	c.owner = 0
	c.supervisor = 0
	c.mode = "manual"
	c.direction = "stop"
	c.lastInput = time.Time{}
	c.mu.Unlock()
	_ = c.send("stop")
}

func (c *Coordinator) fail(reason string) {
	c.Stop()
	c.mu.Lock()
	c.fault = reason
	c.mu.Unlock()
}

func (c *Coordinator) SetGuards(videoHealthy, autoReady func() bool) {
	c.mu.Lock()
	c.videoHealthy = videoHealthy
	c.autoReady = autoReady
	c.mu.Unlock()
}

func (c *Coordinator) Supervise(owner uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transitioning || (c.owner != 0 && c.owner != owner) {
		return ErrBusy
	}
	c.owner = owner
	c.lastSupervisor = time.Now()
	return nil
}

func (c *Coordinator) IsOperator(owner uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return owner != 0 && c.owner == owner && time.Since(c.lastSupervisor) < 700*time.Millisecond
}

func (c *Coordinator) SetMode(owner uint64, mode string) error {
	if mode != "manual" && mode != "auto" {
		return errors.New("mode must be manual or auto")
	}
	c.mu.Lock()
	allowed := owner != 0 && !c.transitioning && c.owner == owner && time.Since(c.lastSupervisor) < 700*time.Millisecond
	videoHealthy, autoReady := c.videoHealthy, c.autoReady
	if allowed {
		c.transitioning = true
		c.transitionCancelled = false
	}
	c.mu.Unlock()
	if !allowed {
		return ErrBusy
	}
	c.stop(false)
	if mode == "auto" && (videoHealthy == nil || autoReady == nil || !videoHealthy() || !autoReady()) {
		c.mu.Lock()
		c.transitioning = false
		c.transitionCancelled = false
		c.mu.Unlock()
		return ErrAutoUnavailable
	}
	if mode == "auto" {
		ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
		err := c.motor.Speed(ctx, 85)
		cancel()
		if err != nil {
			c.mu.Lock()
			c.transitioning = false
			c.transitionCancelled = false
			c.mu.Unlock()
			c.fail("rover control link lost")
			return err
		}
	}
	c.mu.Lock()
	if c.transitionCancelled {
		c.transitioning = false
		c.transitionCancelled = false
		c.mu.Unlock()
		return ErrBusy
	}
	c.mode = mode
	c.owner = owner
	if mode == "auto" {
		c.supervisor = owner
	}
	c.lastSupervisor = time.Now()
	c.fault = ""
	c.transitioning = false
	c.transitionCancelled = false
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) AutoDrive(direction string) error {
	if !validDirection(direction) {
		return ErrDirection
	}
	c.mu.Lock()
	active := c.mode == "auto"
	allowed := c.mode == "auto" && c.supervisor != 0 && time.Since(c.lastSupervisor) < 700*time.Millisecond
	videoHealthy, autoReady := c.videoHealthy, c.autoReady
	if allowed {
		c.direction = direction
		c.lastInput = time.Now()
	}
	c.mu.Unlock()
	if !active {
		return ErrAutoUnavailable
	}
	if !allowed || videoHealthy == nil || autoReady == nil || !videoHealthy() || !autoReady() {
		c.fail("automatic driving signal lost")
		return ErrAutoUnavailable
	}
	if err := c.send(direction); err != nil {
		c.fail("rover control link lost")
		return err
	}
	return nil
}

func (c *Coordinator) FailAuto(reason string) {
	c.mu.Lock()
	active := c.mode == "auto"
	c.mu.Unlock()
	if active {
		c.fail(reason)
	}
}

func (c *Coordinator) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{Mode: c.mode, Direction: c.direction, Occupied: c.owner != 0, Fault: c.fault}
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
			auto := c.mode == "auto"
			lostSupervisor := auto && time.Since(c.lastSupervisor) > 700*time.Millisecond
			videoHealthy, autoReady := c.videoHealthy, c.autoReady
			c.mu.Unlock()
			if auto && (lostSupervisor || videoHealthy == nil || autoReady == nil || !videoHealthy() || !autoReady()) {
				c.fail("automatic driving signal lost")
				continue
			}
			if direction != "stop" && videoHealthy != nil && !videoHealthy() {
				c.fail("video signal lost")
				continue
			}
			if stale {
				if auto {
					c.fail("automatic driving command expired")
				} else {
					c.fail("operator heartbeat expired")
				}
			} else if repeat {
				if err := c.send(direction); err != nil {
					c.fail("rover control link lost")
				}
			}
		}
	}
}
