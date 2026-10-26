// Package sequencer executes validated finite camera-control plans without replay.
package sequencer

import (
	"context"
	"errors"
	"fmt"
	"openmimo/control"
	"time"
)

type Step struct {
	Action *control.Action `json:"action,omitempty"`
	WaitMS int             `json:"wait_ms,omitempty"`
}
type Plan struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Repeat  int    `json:"repeat"`
	Steps   []Step `json:"steps"`
}
type Progress struct {
	Name      string `json:"name"`
	Iteration int    `json:"iteration"`
	Step      int    `json:"step"`
	Total     int    `json:"total"`
}

func (p Plan) Validate() error {
	if p.Version != 1 || p.Repeat < 1 || p.Repeat > 1000 || len(p.Steps) < 1 || len(p.Steps) > 256 || p.Repeat*len(p.Steps) > 5000 {
		return errors.New("plan requires version 1, 1..256 steps, 1..1000 repeats, at most 5000 total steps")
	}
	var waits int64
	for i, s := range p.Steps {
		if (s.Action == nil) == (s.WaitMS == 0) || s.WaitMS < 0 || s.WaitMS > 600000 {
			return fmt.Errorf("step %d requires exactly one action or a wait of 1..600000 ms", i+1)
		}
		if s.Action != nil {
			if _, err := s.Action.Command(); err != nil {
				return fmt.Errorf("step %d: %w", i+1, err)
			}
			if s.Action.Type == "gimbal-jog" {
				waits += int64(s.Action.DurationMS)
			}
		} else {
			waits += int64(s.WaitMS)
		}
	}
	if waits*int64(p.Repeat) > 3600000 {
		return errors.New("plan scheduled waits exceed one hour")
	}
	return nil
}

// Run validates the entire plan before its first side effect. Cancellation and
// errors stop subsequent steps; actions are never automatically retried.
func Run(ctx context.Context, p Plan, execute func(context.Context, control.Action) error, progress func(Progress)) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if execute == nil {
		return errors.New("nil action executor")
	}
	for n := 0; n < p.Repeat; n++ {
		for i, s := range p.Steps {
			if err := ctx.Err(); err != nil {
				return err
			}
			if progress != nil {
				progress(Progress{p.Name, n + 1, i + 1, p.Repeat * len(p.Steps)})
			}
			if s.Action != nil {
				if err := execute(ctx, *s.Action); err != nil {
					return fmt.Errorf("iteration %d step %d: %w", n+1, i+1, err)
				}
			} else {
				timer := time.NewTimer(time.Duration(s.WaitMS) * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
	}
	return nil
}
