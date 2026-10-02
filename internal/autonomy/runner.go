package autonomy

import (
	"context"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/vision"
)

type Controller interface {
	Status() control.Status
	AutoDrive(string) error
	FailAuto(string)
}

type Vision interface {
	Status() vision.Status
	ReadyForAuto() bool
}

// Run is an intentionally conservative supervised experiment. It never plans
// around a suspected obstacle: uncertainty disarms auto and requires a human.
func Run(ctx context.Context, controller Controller, sight Vision) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			controller.FailAuto("automatic runner stopped")
			return
		case <-ticker.C:
		}
		if controller.Status().Mode != "auto" {
			continue
		}
		cycle(ctx, controller, sight, 150*time.Millisecond, 750*time.Millisecond)
	}
}

func cycle(ctx context.Context, controller Controller, sight Vision, pulse, recheck time.Duration) {
	if !sight.ReadyForAuto() {
		controller.FailAuto("visual tracking or clearance uncertain")
		return
	}
	if err := controller.AutoDrive("forward"); err != nil {
		return
	}
	timer := time.NewTimer(pulse)
	select {
	case <-ctx.Done():
		timer.Stop()
	case <-timer.C:
	}
	if err := controller.AutoDrive("stop"); err != nil {
		return
	}
	if ctx.Err() != nil {
		controller.FailAuto("automatic runner stopped")
		return
	}
	baseline := sight.Status().RiskSeq
	deadline := time.NewTimer(recheck)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			controller.FailAuto("automatic runner stopped")
			return
		case <-deadline.C:
			controller.FailAuto("visual recheck timed out")
			return
		case <-ticker.C:
			if controller.Status().Mode != "auto" {
				return
			}
			if !sight.ReadyForAuto() {
				controller.FailAuto("visual tracking or clearance uncertain")
				return
			}
			if sight.Status().RiskSeq > baseline {
				return
			}
		}
	}
}
