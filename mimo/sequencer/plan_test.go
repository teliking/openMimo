package sequencer

import (
	"context"
	"errors"
	"openmimo/control"
	"testing"
	"time"
)

func TestValidateEntirePlanBeforeExecution(t *testing.T) {
	called := 0
	p := Plan{Version: 1, Repeat: 1, Steps: []Step{{Action: &control.Action{Type: "photo"}}, {Action: &control.Action{Type: "iso", ISO: 123}}}}
	if err := Run(context.Background(), p, func(context.Context, control.Action) error { called++; return nil }, nil); err == nil || called != 0 {
		t.Fatal("invalid later step caused partial execution")
	}
}
func TestFailureDoesNotRetryOrContinue(t *testing.T) {
	sentinel := errors.New("uncertain camera outcome")
	called := 0
	p := Plan{Version: 1, Repeat: 3, Steps: []Step{{Action: &control.Action{Type: "photo"}}, {Action: &control.Action{Type: "record-start"}}}}
	err := Run(context.Background(), p, func(context.Context, control.Action) error { called++; return sentinel }, nil)
	if !errors.Is(err, sentinel) || called != 1 {
		t.Fatalf("retry/continuation after error: %d %v", called, err)
	}
}
func TestCancellationInterruptsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := 0
	p := Plan{Version: 1, Repeat: 1, Steps: []Step{{Action: &control.Action{Type: "photo"}}, {WaitMS: 600000}, {Action: &control.Action{Type: "photo"}}}}
	start := time.Now()
	err := Run(ctx, p, func(context.Context, control.Action) error { called++; cancel(); return nil }, nil)
	if !errors.Is(err, context.Canceled) || called != 1 || time.Since(start) > time.Second {
		t.Fatal("cancellation did not stop later steps")
	}
}
func TestRejectUnboundedOrAmbiguousPlans(t *testing.T) {
	for _, p := range []Plan{{Version: 1, Repeat: 1}, {Version: 1, Repeat: 1001, Steps: []Step{{WaitMS: 1}}}, {Version: 1, Repeat: 7, Steps: []Step{{WaitMS: 600000}}}, {Version: 1, Repeat: 1, Steps: []Step{{WaitMS: 1, Action: &control.Action{Type: "photo"}}}}} {
		if p.Validate() == nil {
			t.Fatal("invalid plan accepted")
		}
	}
}
