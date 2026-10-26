package ble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeGATT struct {
	mu            sync.Mutex
	notifications chan Notification
	subscribed    map[string]bool
	stream        duml.Stream
	frames        []duml.Frame
	chunks        int
	closed        int
	manual        bool
	deny          bool
	approvalACK   bool
}

func newGATT() *fakeGATT {
	return &fakeGATT{notifications: make(chan Notification, 128), subscribed: make(map[string]bool)}
}
func (f *fakeGATT) Subscribe(_ context.Context, characteristic string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribed[characteristic] = true
	return nil
}
func (f *fakeGATT) Notifications() <-chan Notification { return f.notifications }
func (f *fakeGATT) Close() error                       { f.mu.Lock(); f.closed++; f.mu.Unlock(); return nil }
func (f *fakeGATT) notify(frame duml.Frame) {
	raw, _ := frame.MarshalBinary()
	// Exercise reassembly on both characteristics with a split inside the header.
	for _, part := range [][]byte{raw[:2], raw[2:8], raw[8:]} {
		f.notifications <- Notification{Characteristic: pocket3.BLENotify, Data: append([]byte(nil), part...)}
	}
}
func (f *fakeGATT) Write(_ context.Context, characteristic string, data []byte, withoutResponse bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.subscribed[pocket3.BLENotify] || !f.subscribed[pocket3.BLEWrite] {
		return errors.New("write before notification subscription")
	}
	if characteristic == pocket3.BLENotify {
		if withoutResponse || !bytes.Equal(data, []byte{1, 0}) {
			return errors.New("bad pairing arm")
		}
		return nil
	}
	if characteristic != pocket3.BLEWrite || !withoutResponse || len(data) > 7 {
		return errors.New("bad GATT fragmentation/write mode")
	}
	f.chunks++
	for _, frame := range f.stream.Feed(data) {
		f.frames = append(f.frames, frame)
		reply := duml.Frame{Sender: frame.Receiver, Receiver: frame.Sender, Sequence: frame.Sequence, CommandSet: frame.CommandSet, CommandID: frame.CommandID, Flags: 0x80}
		switch {
		case frame.CommandSet == 7 && frame.CommandID == 0x45:
			reply.Payload = []byte{0, 1}
			if f.manual {
				reply.Payload[1] = 2
			}
			f.notify(reply)
			if f.manual {
				p := byte(1)
				if f.deny {
					p = 0
				}
				f.notify(duml.Frame{Sender: 7, Receiver: 2, Sequence: 0x3412, Flags: 0x40, CommandSet: 7, CommandID: 0x46, Payload: []byte{p, 0x23}})
			}
		case frame.CommandSet == 7 && frame.CommandID == 0x46:
			f.approvalACK = frame.Sequence == 0x3412 && frame.Flags == 0xc0 && len(frame.Payload) == 2 && frame.Payload[1] == 0x23
		case frame.CommandSet == 7 && (frame.CommandID == 7 || frame.CommandID == 0x0e):
			if f.manual && !f.approvalACK {
				return errors.New("credentials queried before approval ACK")
			}
			value := "test-pocket3"
			if frame.CommandID == 0x0e {
				value = "test-password"
			}
			packed, _ := pocket3.PackString(value)
			reply.Payload = append([]byte{0}, packed...)
			f.notify(reply)
		}
	}
	return nil
}

func TestHandoffAlreadyPairedAndApproval(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "already-paired", true: "camera-approval"}[manual], func(t *testing.T) {
			gatt := newGATT()
			gatt.manual = manual
			client, err := Start(context.Background(), gatt, Config{Identifier: "test-client", MaxWrite: 7, Step: 2 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { client.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			credentials, err := client.Credentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if credentials.SSID != "test-pocket3" || credentials.Password != "test-password" {
				t.Fatal("incorrect credentials")
			}
			encoded, _ := json.Marshal(credentials)
			if strings.Contains(string(encoded), "test-password") || strings.Contains(credentials.String(), "test-password") {
				t.Fatal("credentials leaked in routine output")
			}
			client.Close()
			client.Close()
			gatt.mu.Lock()
			defer gatt.mu.Unlock()
			if gatt.closed != 1 {
				t.Fatalf("adapter closed %d times", gatt.closed)
			}
			if manual && !gatt.approvalACK {
				t.Fatal("approval not acknowledged")
			}
			if gatt.chunks <= len(gatt.frames) {
				t.Fatal("fragmentation not exercised")
			}
		})
	}
}
func TestHandoffDenied(t *testing.T) {
	gatt := newGATT()
	gatt.manual, gatt.deny = true, true
	client, err := Start(context.Background(), gatt, Config{Identifier: "test", MaxWrite: 7, Step: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = client.Credentials(ctx); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("denial: %v", err)
	}
	gatt.mu.Lock()
	defer gatt.mu.Unlock()
	for _, frame := range gatt.frames {
		if frame.CommandSet == 0x53 {
			t.Fatal("AP woken after denial")
		}
	}
}
func TestHandoffCancellation(t *testing.T) {
	gatt := newGATT()
	ctx, cancel := context.WithCancel(context.Background())
	client, err := Start(ctx, gatt, Config{Identifier: "test", MaxWrite: 7, Step: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	wait, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if _, err = client.Credentials(wait); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	client.Close()
}

func TestTimingOverflowRejected(t *testing.T) {
	if _, err := Start(context.Background(), newGATT(), Config{Step: time.Duration(1<<63 - 1)}); err == nil {
		t.Fatal("accepted overflowing BLE timers")
	}
}
