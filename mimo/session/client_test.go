package session

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"openmimo/protocol/datalink"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"sync"
	"testing"
	"time"
)

type fakeCamera struct {
	conn               net.PacketConn
	mu                 sync.Mutex
	counts             map[byte]int
	transportSequences []uint16
	routes             []datalink.Routing
	frames             []duml.Frame
}

func newFakeCamera(t *testing.T) *fakeCamera {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCamera{conn: conn, counts: make(map[byte]int)}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 65535)
		peerSequence := uint16(0x2020)
		for {
			n, remote, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			d, err := datalink.Parse(buffer[:n])
			if err != nil {
				continue
			}
			send := func(packet datalink.Datagram) { b, _ := packet.MarshalBinary(); conn.WriteTo(b, remote) }
			if d.Type == datalink.Handshake {
				send(datalink.Datagram{Session: d.Session, Type: datalink.Handshake, Payload: []byte{0x20, 0x20}})
				continue
			}
			if d.Type != datalink.Command {
				continue
			}
			route, err := datalink.ParseRouting(d.Payload)
			if err != nil {
				continue
			}
			frames := duml.Scan(d.Payload[12:])
			if len(frames) != 1 {
				continue
			}
			frame := frames[0]
			f.mu.Lock()
			f.counts[frame.CommandID]++
			f.transportSequences = append(f.transportSequences, d.Sequence)
			f.routes = append(f.routes, route)
			f.frames = append(f.frames, frame)
			f.mu.Unlock()
			status := make([]byte, 26)
			binary.LittleEndian.PutUint16(status, 0x2020)
			binary.LittleEndian.PutUint16(status[16:], d.Sequence)
			send(datalink.Datagram{Session: d.Session, Type: datalink.Status, Payload: status})
			if frame.CommandSet == 0 && frame.CommandID == 0x99 {
				nameLength := int(binary.LittleEndian.Uint16(frame.Payload[13:]))
				name := string(frame.Payload[15 : 15+nameLength])
				if name == "missing" {
					continue
				}
				tx := binary.LittleEndian.Uint32(frame.Payload[4:])
				push := func(name string, transaction uint32, flags byte, value []byte) {
					payload := make([]byte, 23+len(name)+len(value))
					payload[0], payload[1] = 2, 6
					binary.LittleEndian.PutUint32(payload[4:], transaction)
					binary.LittleEndian.PutUint16(payload[13:], uint16(len(name)))
					copy(payload[15:], name)
					binary.LittleEndian.PutUint16(payload[21+len(name):], uint16(len(value)))
					copy(payload[23+len(name):], value)
					raw, _ := (duml.Frame{Sender: 0x28, Receiver: 2, Sequence: 0x9000, Flags: flags, CommandSet: 0, CommandID: 0x99, Payload: payload}).MarshalBinary()
					peerSequence += 8
					send(datalink.Datagram{Session: d.Session, Sequence: peerSequence, Type: datalink.Data, Payload: append(make([]byte, 12), raw...)})
				}
				if name == "observed-only" {
					push("wrong-name", 77, 0, []byte("wrong"))
					push(name, 77, 1, []byte("encrypted"))
					push(name, 77, 0, []byte("live-value"))
					continue
				}
				push(name, tx-1, 0, []byte("stale"))
				push("wrong-name", tx, 0, []byte("wrong-name"))
				push(name, tx, 1, []byte("encrypted"))
				push(name, tx, 0, []byte(name))
				continue
			}
			if frame.Flags&0x80 != 0 || frame.Flags&0x60 == 0 || frame.CommandID == 0xfd {
				continue
			}
			payload := frame.Payload
			if frame.CommandSet == 4 && frame.CommandID == 0x50 {
				payload = []byte{0}
			}
			reply := duml.Frame{Sender: frame.Receiver, Receiver: frame.Sender, Sequence: frame.Sequence, Flags: 0x80, CommandSet: frame.CommandSet, CommandID: frame.CommandID, Payload: payload}
			if frame.CommandID == 0xfe {
				wrong := reply
				wrong.Payload = []byte{0xee}
				raw, _ := wrong.MarshalBinary()
				send(datalink.Datagram{Session: d.Session + 1, Type: datalink.Data, Payload: raw})
				wrong.CommandID--
				raw, _ = wrong.MarshalBinary()
				send(datalink.Datagram{Session: d.Session, Type: datalink.Data, Payload: raw})
				wrong.CommandID++
				wrong.Flags = 0x81
				raw, _ = wrong.MarshalBinary()
				send(datalink.Datagram{Session: d.Session, Type: datalink.Data, Payload: raw})
			}
			raw, _ := reply.MarshalBinary()
			peerSequence += 8
			send(datalink.Datagram{Session: d.Session, Sequence: peerSequence, Type: datalink.Data, Payload: append(make([]byte, 12), raw...)})
		}
	}()
	return f
}
func connectFake(t *testing.T, f *fakeCamera) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := Dial(ctx, Config{Address: f.conn.LocalAddr().String(), SkipTCPBootstrap: true, HandshakeAttempts: 2, HandshakeInterval: 10 * time.Millisecond, SettleInterval: time.Millisecond, RequestTimeout: time.Second, KeepaliveInterval: time.Hour, SessionID: 0x1234, BaseSequence: 0x2000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func TestSessionConcurrentRequestsAndForeignReplies(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	var group sync.WaitGroup
	for i := 0; i < 200; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := pocket3.Command{Receiver: 1, Set: 2, ID: 0xfe, Flags: 0x40, Payload: []byte{byte(i)}}
			reply, err := c.Request(ctx, command)
			if err != nil {
				t.Error(err)
				return
			}
			if len(reply.Payload) != 1 || reply.Payload[0] != byte(i) {
				t.Errorf("request %d received unrelated payload %x", i, reply.Payload)
			}
		}(i)
	}
	group.Wait()
	s, err := c.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.ForeignSession != 200 {
		t.Fatalf("foreign session count %d", s.ForeignSession)
	}
	if s.DroppedEvents == 0 {
		t.Fatal("event overflow did not occur")
	}
	if !s.Healthy(time.Now()) {
		t.Fatal("readiness probe not observed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, sequence := range f.transportSequences {
		if sequence != 0x2028+uint16(i)*8 {
			t.Fatalf("transport sequence[%d]=%x", i, sequence)
		}
		if f.routes[i].Sequence != sequence {
			t.Fatal("routing/transport sequence differs")
		}
		if i > 0 && datalink.Newer(f.routes[i].PeerAck, f.transportSequences[i-1]) {
			t.Fatal("routing advertised unacknowledged sequence")
		}
	}
}
func TestTimeoutDoesNotReplayAction(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := c.Request(ctx, pocket3.Command{Receiver: 1, Set: 2, ID: 0xfd, Flags: 0x40})
	if !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout: %v", err)
	}
	time.Sleep(70 * time.Millisecond)
	f.mu.Lock()
	count := f.counts[0xfd]
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("non-idempotent action sent %d times", count)
	}
	s, _ := c.Stats(context.Background())
	if s.Pending != 0 {
		t.Fatalf("pending requests leaked: %d", s.Pending)
	}
}
func TestCloseWakesPendingRequests(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	finished := make(chan error, 1)
	go func() {
		_, err := c.Request(context.Background(), pocket3.Command{Receiver: 1, Set: 2, ID: 0xfd, Flags: 0x40})
		finished <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		count := f.counts[0xfd]
		f.mu.Unlock()
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not reach fake camera")
		}
		time.Sleep(time.Millisecond)
	}
	c.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close stranded a request")
	}
}
func TestHandshakeCancellation(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err = Dial(ctx, Config{Address: conn.LocalAddr().String(), SkipTCPBootstrap: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake ignored cancellation: %v", err)
	}
}

func TestCloseStopsActiveGimbal(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	joystick, err := pocket3.Joystick(20, -40)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Send(context.Background(), joystick); err != nil {
		t.Fatal(err)
	}
	if err = c.Send(context.Background(), (pocket3.SpeedPayload{Yaw: 15}).Command()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		joystickStopped, speedStopped := false, false
		for _, frame := range f.frames {
			if frame.CommandSet != 4 {
				continue
			}
			if frame.CommandID == 1 && len(frame.Payload) == 10 {
				joystickStopped = binary.LittleEndian.Uint16(frame.Payload) == 1024 && binary.LittleEndian.Uint16(frame.Payload[4:]) == 1024
			}
			if frame.CommandID == 0x0c && len(frame.Payload) == 8 {
				speedStopped = binary.LittleEndian.Uint16(frame.Payload) == 0 && binary.LittleEndian.Uint16(frame.Payload[2:]) == 0 && binary.LittleEndian.Uint16(frame.Payload[4:]) == 0
			}
		}
		f.mu.Unlock()
		if joystickStopped && speedStopped {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("shutdown did not neutralize joystick=%v speed=%v", joystickStopped, speedStopped)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReceiveWindowsSeedThenTrackDirectPackets(t *testing.T) {
	s := Stats{Window: datalink.AckWindow{RxType2: 0x1000, RxType3: 0x1000, PeerAckTx: 0x2000, LastTx: 0x2020}}
	status := make([]byte, 26)
	binary.LittleEndian.PutUint16(status[2:], 0xe000)
	binary.LittleEndian.PutUint16(status[10:], 0xd000)
	binary.LittleEndian.PutUint16(status[16:], 0x2010)
	observe(&s, datalink.Datagram{Type: datalink.Status, Payload: status})
	if s.Window.RxType2 != 0xe000 || s.Window.RxType3 != 0xd000 || s.Window.PeerAckTx != 0x2010 {
		t.Fatal("status did not seed independent windows")
	}
	observe(&s, datalink.Datagram{Type: datalink.Media, Sequence: 0xfff8})
	observe(&s, datalink.Datagram{Type: datalink.Media, Sequence: 0})
	observe(&s, datalink.Datagram{Type: datalink.Data, Sequence: 0x0010})
	// Status must not overwrite a directly observed stream, nor ACK future TX.
	binary.LittleEndian.PutUint16(status[16:], 0x2030)
	observe(&s, datalink.Datagram{Type: datalink.Status, Payload: status})
	observe(&s, datalink.Datagram{Type: datalink.Media, Sequence: 0xfff8})
	if s.Window.RxType2 != 0 || s.Window.RxType3 != 0x10 || s.Window.PeerAckTx != 0x2010 {
		t.Fatalf("window regression: %+v", s.Window)
	}
}

func TestConcurrentPropertyReadsMatchTransactionAndName(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	var group sync.WaitGroup
	for i := 0; i < 30; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			name := fmt.Sprintf("property%d", i%3)
			p, err := c.ReadProperty(context.Background(), name)
			if err != nil {
				t.Error(err)
				return
			}
			if p.Name != name || string(p.Value) != name {
				t.Errorf("unrelated property: %+v", p)
			}
		}(i)
	}
	group.Wait()
	s, err := c.Stats(context.Background())
	if err != nil || s.Pending != 0 {
		t.Fatalf("pending property waiters: %+v %v", s, err)
	}
}
func TestObservePropertyWithFirmwarePushCounter(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.ReadProperty(ctx, "observed-only"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("strict request accepted unrelated counter: %v", err)
	}
	p, err := c.ObserveProperty(context.Background(), "observed-only")
	if err != nil || p.Transaction != 77 || string(p.Value) != "live-value" {
		t.Fatalf("observation: %+v %v", p, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if _, err := c.ObserveProperty(ctx2, "missing"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("observation returned unrelated cached state: %v", err)
	}
}
func TestPropertyReadCancellationAndShutdown(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if _, err := c.ReadProperty(ctx, "missing"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := c.ReadProperty(context.Background(), "missing"); finished <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		count := f.counts[0x99]
		f.mu.Unlock()
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("property request never sent")
		}
		time.Sleep(time.Millisecond)
	}
	c.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stranded property waiter")
	}
}
func TestRejectEncryptedCommandsAndInvalidSessionConfig(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	if err := c.Send(context.Background(), pocket3.Command{Flags: 1}); err == nil {
		t.Fatal("accepted unsupported encryption")
	}
	for _, config := range []Config{{BaseSequence: 9}, {TCPPort: 65536}} {
		if _, err := config.defaults(); err == nil {
			t.Fatal("accepted invalid session config")
		}
	}
}

func TestTransportFailureRetainsCauseAndUnknownActionOutcome(t *testing.T) {
	f := newFakeCamera(t)
	c := connectFake(t, f)
	finished := make(chan error, 1)
	go func() {
		_, err := c.Request(context.Background(), pocket3.Command{Receiver: 1, Set: 2, ID: 0xfd, Flags: 0x40})
		finished <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		sent := f.counts[0xfd] > 0
		f.mu.Unlock()
		if sent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("action not sent")
		}
		time.Sleep(time.Millisecond)
	}
	c.conn.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, ErrClosed) || !errors.Is(err, net.ErrClosed) {
			t.Fatalf("lost failure semantics: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("transport failure stranded waiter")
	}
	c.Close()
	if !errors.Is(c.Err(), net.ErrClosed) {
		t.Fatalf("lost retained cause: %v", c.Err())
	}
}
