package mobilecore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"openmimo/control"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"openmimo/sequencer"
	"openmimo/session"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestJSONBoundaryAndOfflineActions(t *testing.T) {
	e := New()
	for _, input := range []string{`{"op":"action","params":{"type":"mode","mode":999}}`, `{"op":"action","params":{"type":"photo"}}`, `{"op":"script-start","params":{"version":1,"name":"test","repeat":1,"steps":[{"action":{"type":"photo"}}]}}`, `{"op":"status","typo":1}`, `{"op":"status"}{}`} {
		var result response
		if err := json.Unmarshal(e.Dispatch([]byte(input)), &result); err != nil || result.OK {
			t.Fatalf("accepted invalid/offline action %s: %s", input, result.Error)
		}
	}
	var self response
	if err := json.Unmarshal(e.Dispatch([]byte(`{"op":"self-test"}`)), &self); err != nil || !self.OK {
		t.Fatal("offline checks failed")
	}
}
func TestSocketBinderUsesExplicitHandleWithoutSending(t *testing.T) {
	e := New()
	called := false
	e.SetSocketBinder(func(handle uint64, raw syscall.RawConn) error {
		called = true
		if handle != 1234 {
			return errors.New("wrong handle")
		}
		return raw.Control(func(fd uintptr) {
			if fd == 0 {
				t.Error("invalid descriptor")
			}
		})
	})
	var result response
	json.Unmarshal(e.Dispatch([]byte(`{"op":"network-check","params":{"handle":1234}}`)), &result)
	if !result.OK || !called {
		t.Fatal(result.Error)
	}
}
func TestGATTOperationRequiresMatchingAcknowledgement(t *testing.T) {
	operations := make(chan Event, 1)
	g := newGATT(func(ctx context.Context, event Event) error { operations <- event; return nil })
	defer g.Close()
	done := make(chan error, 1)
	go func() { done <- g.Write(context.Background(), "fff5", []byte{1, 2}, true) }()
	event := <-operations
	data := event.Data.(map[string]any)
	id := data["id"].(uint64)
	g.Ack(id+1, "")
	select {
	case <-done:
		t.Fatal("foreign operation ACK accepted")
	case <-time.After(10 * time.Millisecond):
	}
	g.Ack(id, "")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("matching operation did not complete")
	}
}
func TestGATTCloseWakesWritesAndSuppressesFutureOperations(t *testing.T) {
	operations := make(chan Event, 1)
	g := newGATT(func(ctx context.Context, event Event) error { operations <- event; return nil })
	done := make(chan error, 1)
	go func() { done <- g.Subscribe(context.Background(), "fff4") }()
	<-operations
	g.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed operation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close stranded writer")
	}
	if err := g.Write(context.Background(), "fff5", nil, false); err == nil {
		t.Fatal("write accepted after close")
	}
	select {
	case <-operations:
		t.Fatal("operation emitted after close")
	default:
	}
}
func TestStatusSerializationDuringPropertyUpdates(t *testing.T) {
	e := New()
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 100; n++ {
				e.mu.Lock()
				e.properties["test"] = map[string]int{"value": n}
				e.mu.Unlock()
				var result response
				if err := json.Unmarshal(e.Dispatch([]byte(`{"op":"status"}`)), &result); err != nil || !result.OK {
					t.Error("status JSON corrupted")
				}
			}
		}()
	}
	group.Wait()
}

type fakeSession struct {
	sent   chan pocket3.Command
	events chan session.Event
	once   sync.Once
}

func newFakeSession() *fakeSession {
	return &fakeSession{sent: make(chan pocket3.Command, 64), events: make(chan session.Event)}
}
func (f *fakeSession) Send(ctx context.Context, c pocket3.Command) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case f.sent <- c:
		return nil
	}
}
func (f *fakeSession) Request(ctx context.Context, c pocket3.Command) (duml.Frame, error) {
	if err := f.Send(ctx, c); err != nil {
		return duml.Frame{}, err
	}
	return duml.Frame{Flags: 0x80, Payload: []byte{0}}, nil
}
func (f *fakeSession) ReadProperty(context.Context, string) (pocket3.PropertyPush, error) {
	return pocket3.PropertyPush{}, errors.New("unused fake property")
}
func (f *fakeSession) ObserveProperty(ctx context.Context, name string) (pocket3.PropertyPush, error) {
	return f.ReadProperty(ctx, name)
}
func (f *fakeSession) Stats(context.Context) (session.Stats, error) { return session.Stats{}, nil }
func (f *fakeSession) Events() <-chan session.Event                 { return f.events }
func (f *fakeSession) Err() error                                   { return session.ErrClosed }
func (f *fakeSession) Close() error                                 { f.once.Do(func() { close(f.events) }); return nil }

func TestJogWatchdogNeutralizesWithoutFurtherUIUpdates(t *testing.T) {
	e := New()
	f := newFakeSession()
	e.client = f
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.jogLoop(ctx, f); close(done) }()
	if err := e.jog(100, -100); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	sawMotion := false
	for {
		select {
		case command := <-f.sent:
			if command.Set != 4 || command.ID != 1 {
				t.Fatal("unexpected watchdog command")
			}
			neutral := binary.LittleEndian.Uint16(command.Payload) == 1024 && binary.LittleEndian.Uint16(command.Payload[4:]) == 1024
			if neutral {
				if !sawMotion {
					t.Fatal("no actual motion sent")
				}
				cancel()
				<-done
				return
			}
			sawMotion = true
		case <-deadline:
			t.Fatal("watchdog did not send neutral")
		}
	}
}
func TestEngineScriptCancellationLeavesNoSubsequentCapture(t *testing.T) {
	e := New()
	f := newFakeSession()
	e.client = f
	plan := sequencer.Plan{Version: 1, Name: "cancel", Repeat: 1, Steps: []sequencer.Step{{Action: &control.Action{Type: "photo"}}, {WaitMS: 600000}, {Action: &control.Action{Type: "photo"}}}}
	if err := e.startScript(plan); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.sent:
	case <-time.After(time.Second):
		t.Fatal("first action not issued")
	}
	e.cancelScript()
	deadline := time.Now().Add(time.Second)
	for {
		e.mu.Lock()
		state := e.scriptState
		e.mu.Unlock()
		if state == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("script state %s", state)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case command := <-f.sent:
		t.Fatalf("action after cancel: %+v", command)
	default:
	}
}

func TestUnexpectedDisconnectReportsCauseAndDropsCachedCapabilities(t *testing.T) {
	e := New()
	f := newFakeSession()
	e.client = f
	e.properties["camcap_video_format"] = map[string]int{"stale": 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.connectCancel = cancel
	f.Close()
	e.receive(ctx, f, 0)
	e.mu.Lock()
	left := len(e.properties)
	e.mu.Unlock()
	if left != 0 {
		t.Fatal("stale capability cache survived disconnection")
	}
	select {
	case event := <-e.events:
		if event.Kind != "error" {
			t.Fatalf("lost root error before disconnect: %+v", event)
		}
	default:
		t.Fatal("disconnect error suppressed by cleanup cancellation")
	}
}
