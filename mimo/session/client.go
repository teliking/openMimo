// Package session owns a Pocket 3 UDP control session. Network association and
// BLE GATT access belong to platform adapters; camera commands use this session.
package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"openmimo/protocol/datalink"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var ErrClosed = errors.New("Pocket 3 session closed")

// ErrOutcomeUnknown means a command may have reached the camera. Read back its
// resulting state before deciding whether to send a non-idempotent action again.
var ErrOutcomeUnknown = errors.New("camera command outcome unknown")

type Config struct {
	// SocketControl binds sockets to a platform-selected network, before connect.
	SocketControl     func(string, string, syscall.RawConn) error
	Address           string
	TCPPort           int
	Identifier        string
	SkipTCPBootstrap  bool
	HandshakeAttempts int
	HandshakeInterval time.Duration
	SettleInterval    time.Duration
	RequestTimeout    time.Duration
	KeepaliveInterval time.Duration
	SessionID         uint16
	BaseSequence      uint16
}

func (c Config) defaults() (Config, error) {
	if c.Address == "" {
		c.Address = "192.168.2.1:9004"
	}
	if c.TCPPort == 0 {
		c.TCPPort = 7001
	}
	if c.HandshakeAttempts == 0 {
		c.HandshakeAttempts = 20
	}
	if c.HandshakeInterval == 0 {
		c.HandshakeInterval = 350 * time.Millisecond
	}
	if c.SettleInterval == 0 {
		c.SettleInterval = 400 * time.Millisecond
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 2 * time.Second
	}
	if c.KeepaliveInterval == 0 {
		c.KeepaliveInterval = time.Second
	}
	if c.HandshakeAttempts < 1 || c.HandshakeAttempts > 100 || c.HandshakeInterval < 0 || c.SettleInterval < 0 || c.RequestTimeout < 0 || c.KeepaliveInterval < 0 {
		return c, errors.New("invalid session timing configuration")
	}
	if c.TCPPort < 1 || c.TCPPort > 65535 || c.BaseSequence&7 != 0 {
		return c, errors.New("invalid TCP port or unaligned base sequence")
	}
	var random [20]byte
	if _, err := rand.Read(random[:]); err != nil {
		return c, err
	}
	if c.SessionID == 0 {
		c.SessionID = 0x1000 + binary.LittleEndian.Uint16(random[:])%0xeffe
	}
	if c.BaseSequence == 0 {
		c.BaseSequence = (0x1000 + binary.LittleEndian.Uint16(random[2:])%0xe000) & 0xfff8
	}
	if c.Identifier == "" {
		c.Identifier = hex.EncodeToString(random[4:])
	}
	return c, nil
}

type Event struct {
	PacketType byte
	Sequence   uint16
	Frame      *duml.Frame
	Media      []byte
}
type Stats struct {
	SessionID      uint16
	BaseSequence   uint16
	CameraChannel  uint16
	NextTx         uint16
	Window         datalink.AckWindow
	LastInbound    time.Time
	LastProbeReply time.Time
	Pending        int
	Malformed      uint64
	ForeignSession uint64
	DroppedEvents  uint64
	seenType2      bool
	seenType3      bool
}

func (s Stats) Healthy(now time.Time) bool {
	return !s.LastProbeReply.IsZero() && now.Sub(s.LastProbeReply) < 3*time.Second && now.Sub(s.LastInbound) < 3*time.Second
}

type result struct {
	frame duml.Frame
	err   error
}
type sendRequest struct {
	ctx     context.Context
	command pocket3.Command
	wait    bool
	result  chan result
}
type snapshotRequest struct{ result chan Stats }
type inbound struct {
	data []byte
	err  error
}
type pending struct {
	request  sendRequest
	receiver byte
	set      byte
	id       byte
}

type Client struct {
	cfg         Config
	conn        net.Conn
	stop        chan struct{}
	done        chan struct{}
	requests    chan sendRequest
	properties  chan propertyRequest
	snapshots   chan snapshotRequest
	events      chan Event
	once        sync.Once
	errMu       sync.Mutex
	terminalErr error
}

// Dial performs TCP bootstrap, UDP handshake, APP registration and a gimbal
// readiness probe. Its context bounds connection setup; Close owns its lifetime.
func Dial(ctx context.Context, config Config) (*Client, error) {
	cfg, err := config.defaults()
	if err != nil {
		return nil, err
	}
	if !cfg.SkipTCPBootstrap {
		if err = bootstrap(ctx, cfg); err != nil {
			return nil, fmt.Errorf("TCP bootstrap: %w", err)
		}
	}
	conn, err := (&net.Dialer{Control: cfg.SocketControl}).DialContext(ctx, "udp", cfg.Address)
	if err != nil {
		return nil, err
	}
	state := Stats{SessionID: cfg.SessionID, BaseSequence: cfg.BaseSequence, CameraChannel: cfg.BaseSequence, Window: datalink.AckWindow{RxType2: cfg.BaseSequence, RxType3: cfg.BaseSequence, PeerAckTx: cfg.BaseSequence, LastTx: cfg.BaseSequence}}
	if err = handshake(ctx, conn, cfg, &state); err != nil {
		conn.Close()
		return nil, err
	}
	state.NextTx = state.CameraChannel + 8
	state.Window.LastTx = state.CameraChannel
	state.Window.PeerAckTx = state.CameraChannel
	c := &Client{cfg: cfg, conn: conn, stop: make(chan struct{}), done: make(chan struct{}), requests: make(chan sendRequest), properties: make(chan propertyRequest), snapshots: make(chan snapshotRequest), events: make(chan Event, 128)}
	go c.run(state)
	for _, command := range []pocket3.Command{pocket3.AppDeviceInfo(), pocket3.AppPresence(), pocket3.GimbalInitialize()} {
		if err = c.Send(ctx, command); err != nil {
			c.Close()
			return nil, err
		}
		if err = pause(ctx, cfg.SettleInterval); err != nil {
			c.Close()
			return nil, err
		}
	}
	reply, err := c.Request(ctx, pocket3.GimbalProbe())
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("gimbal readiness: %w", err)
	}
	if len(reply.Payload) == 0 {
		c.Close()
		return nil, errors.New("empty gimbal readiness reply")
	}
	return c, nil
}

func bootstrap(ctx context.Context, cfg Config) error {
	host, _, err := net.SplitHostPort(cfg.Address)
	if err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()
	conn, err := (&net.Dialer{Control: cfg.SocketControl}).DialContext(connectCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(cfg.TCPPort)))
	if err != nil {
		return err
	}
	defer conn.Close()
	command, err := pocket3.Pair(cfg.Identifier, "osmo")
	if err != nil {
		return err
	}
	frame, err := command.Frame(0x8092).MarshalBinary()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(cfg.RequestTimeout)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err = conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	for len(frame) > 0 {
		n, err := conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return pause(ctx, cfg.SettleInterval)
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func writeDatagram(conn net.Conn, packet datalink.Datagram) error {
	data, err := packet.MarshalBinary()
	if err != nil {
		return err
	}
	n, err := conn.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func observe(s *Stats, d datalink.Datagram) {
	s.LastInbound = time.Now()
	if len(d.Payload) >= 2 {
		channel := binary.LittleEndian.Uint16(d.Payload)
		if channel != 0 {
			s.CameraChannel = channel
		}
	}
	if d.Type == datalink.Media {
		if !s.seenType2 || datalink.Newer(d.Sequence, s.Window.RxType2) {
			s.Window.RxType2 = d.Sequence
		}
		s.seenType2 = true
	}
	if d.Type == datalink.Data {
		if !s.seenType3 || datalink.Newer(d.Sequence, s.Window.RxType3) {
			s.Window.RxType3 = d.Sequence
		}
		s.seenType3 = true
	}
	if d.Type == datalink.Status && len(d.Payload) >= 26 {
		rx2 := binary.LittleEndian.Uint16(d.Payload[2:])
		rx3 := binary.LittleEndian.Uint16(d.Payload[10:])
		ack := binary.LittleEndian.Uint16(d.Payload[16:])
		if !s.seenType2 && rx2 != 0 {
			s.Window.RxType2 = rx2
		}
		if !s.seenType3 && rx3 != 0 {
			s.Window.RxType3 = rx3
		}
		if datalink.Newer(ack, s.Window.PeerAckTx) && !datalink.Newer(ack, s.Window.LastTx) {
			s.Window.PeerAckTx = ack
		}
	}
}

func handshake(ctx context.Context, conn net.Conn, cfg Config, state *Stats) error {
	buffer := make([]byte, 65535)
	readUntil := func(deadline time.Time, seekHandshake bool) (bool, error) {
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			readDeadline := deadline
			if readDeadline.After(time.Now().Add(50 * time.Millisecond)) {
				readDeadline = time.Now().Add(50 * time.Millisecond)
			}
			if parent, ok := ctx.Deadline(); ok && parent.Before(readDeadline) {
				readDeadline = parent
			}
			conn.SetReadDeadline(readDeadline)
			n, err := conn.Read(buffer)
			if err != nil {
				if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					continue
				}
				return false, err
			}
			d, err := datalink.Parse(buffer[:n])
			if err != nil {
				state.Malformed++
				continue
			}
			if d.Session != cfg.SessionID {
				state.ForeignSession++
				continue
			}
			observe(state, d)
			if seekHandshake && d.Type == datalink.Handshake {
				return true, nil
			}
		}
		return false, nil
	}

	connected := false
	for attempt := 0; attempt < cfg.HandshakeAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		packet := datalink.Datagram{Session: cfg.SessionID, Sequence: state.NextTx, Type: datalink.Handshake, Payload: pocket3.HandshakePayload(cfg.BaseSequence)}
		if err := writeDatagram(conn, packet); err != nil {
			return err
		}
		state.NextTx += 8
		ok, err := readUntil(time.Now().Add(cfg.HandshakeInterval), true)
		if err != nil {
			return err
		}
		if ok {
			connected = true
			break
		}
	}
	if !connected {
		return errors.New("UDP handshake exhausted attempts")
	}
	for i := 0; i < 5; i++ {
		if _, err := readUntil(time.Now().Add(cfg.SettleInterval), false); err != nil {
			return err
		}
		if err := writeDatagram(conn, datalink.Datagram{Session: cfg.SessionID, Type: datalink.Acknowledgement, Payload: state.Window.MarshalBinary()}); err != nil {
			return err
		}
	}
	return conn.SetReadDeadline(time.Time{})
}

func (c *Client) Events() <-chan Event { return c.events }

// Err is nil while the session is running. After shutdown it retains ErrClosed
// and, for transport failures, the underlying network error.
func (c *Client) Err() error { c.errMu.Lock(); defer c.errMu.Unlock(); return c.terminalErr }
func (c *Client) Close() error {
	c.once.Do(func() {
		close(c.stop)
		select {
		case <-c.done:
		case <-time.After(200 * time.Millisecond):
			c.conn.Close()
		}
	})
	<-c.done
	return nil
}
func (c *Client) Stats(ctx context.Context) (Stats, error) {
	request := snapshotRequest{result: make(chan Stats, 1)}
	select {
	case <-ctx.Done():
		return Stats{}, ctx.Err()
	case <-c.done:
		return Stats{}, ErrClosed
	case c.snapshots <- request:
	}
	select {
	case result := <-request.result:
		return result, nil
	case <-ctx.Done():
		return Stats{}, ctx.Err()
	case <-c.done:
		return Stats{}, ErrClosed
	}
}

func (c *Client) Send(ctx context.Context, command pocket3.Command) error {
	_, err := c.submit(ctx, command, false)
	return err
}
func (c *Client) Request(ctx context.Context, command pocket3.Command) (duml.Frame, error) {
	if command.Flags&0x80 != 0 || command.Flags&0x60 == 0 {
		return duml.Frame{}, errors.New("command does not request a response")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	return c.submit(requestCtx, command, true)
}
func (c *Client) submit(ctx context.Context, command pocket3.Command, wait bool) (duml.Frame, error) {
	if command.Flags&7 != 0 {
		return duml.Frame{}, errors.New("session does not implement encrypted DUML payloads")
	}
	command.Payload = append([]byte(nil), command.Payload...)
	request := sendRequest{ctx: ctx, command: command, wait: wait, result: make(chan result, 1)}
	select {
	case <-ctx.Done():
		return duml.Frame{}, ctx.Err()
	case <-c.done:
		return duml.Frame{}, ErrClosed
	case c.requests <- request:
	}
	select {
	case result := <-request.result:
		return result.frame, result.err
	case <-ctx.Done():
		return duml.Frame{}, fmt.Errorf("%w: %w", ErrOutcomeUnknown, ctx.Err())
	case <-c.done:
		select {
		case result := <-request.result:
			return result.frame, result.err
		default:
			return duml.Frame{}, c.Err()
		}
	}
}

func (c *Client) run(state Stats) {
	defer close(c.done)
	defer close(c.events)
	defer c.conn.Close()
	rx := make(chan inbound, 64)
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, err := c.conn.Read(buffer)
			packet := inbound{data: append([]byte(nil), buffer[:n]...), err: err}
			select {
			case rx <- packet:
			case <-c.stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	keepalive := time.NewTicker(c.cfg.KeepaliveInterval)
	defer keepalive.Stop()
	pendingRequests := make(map[uint16]pending)
	pendingProperties := make(map[uint32]propertyRequest)
	nextPropertyTransaction := uint32(0x10000)
	joystickActive, speedActive := false, false
	terminalErr := error(ErrClosed)
	nextDUML := uint16(0xa000)
	counter := byte(0)
	defer func() {
		closedErr := terminalErr
		if !errors.Is(closedErr, ErrClosed) {
			closedErr = fmt.Errorf("%w: %w", ErrClosed, closedErr)
		}
		c.errMu.Lock()
		c.terminalErr = closedErr
		c.errMu.Unlock()
		for _, p := range pendingRequests {
			p.request.result <- result{err: fmt.Errorf("%w: %w", ErrOutcomeUnknown, closedErr)}
		}
		for _, p := range pendingProperties {
			p.result <- propertyResult{err: closedErr}
		}
	}()
	publish := func(event Event) {
		select {
		case c.events <- event:
		default:
			state.DroppedEvents++
		}
	}
	send := func(request sendRequest) error {
		if err := request.ctx.Err(); err != nil {
			request.result <- result{err: err}
			return nil
		}
		sequence := nextDUML
		for attempts := 0; ; attempts++ {
			if _, used := pendingRequests[sequence]; !used {
				break
			}
			if attempts >= 65535 {
				request.result <- result{err: errors.New("all DUML sequence numbers occupied")}
				return nil
			}
			sequence++
		}
		frame := request.command.Frame(sequence)
		raw, err := frame.MarshalBinary()
		if err != nil {
			request.result <- result{err: err}
			return nil
		}
		counter++
		route := (datalink.Routing{PeerAck: state.Window.PeerAckTx, Sequence: state.NextTx, Counter: counter}).MarshalBinary()
		if request.wait {
			pendingRequests[sequence] = pending{request: request, receiver: frame.Receiver, set: frame.CommandSet, id: frame.CommandID}
		}
		deadline := time.Now().Add(c.cfg.RequestTimeout)
		if parent, ok := request.ctx.Deadline(); ok && parent.Before(deadline) {
			deadline = parent
		}
		c.conn.SetWriteDeadline(deadline)
		if err = writeDatagram(c.conn, datalink.Datagram{Session: c.cfg.SessionID, Sequence: state.NextTx, Type: datalink.Command, Payload: append(route, raw...)}); err != nil {
			delete(pendingRequests, sequence)
			request.result <- result{err: fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)}
			return err
		}
		c.conn.SetWriteDeadline(time.Time{})
		state.Window.LastTx = state.NextTx
		state.NextTx += 8
		nextDUML = sequence + 1
		if frame.CommandSet == 4 && frame.CommandID == 1 && len(frame.Payload) == 10 {
			joystickActive = binary.LittleEndian.Uint16(frame.Payload) != 1024 || binary.LittleEndian.Uint16(frame.Payload[4:]) != 1024
		}
		if frame.CommandSet == 4 && frame.CommandID == 0x0c && len(frame.Payload) == 8 {
			speedActive = binary.LittleEndian.Uint16(frame.Payload) != 0 || binary.LittleEndian.Uint16(frame.Payload[2:]) != 0 || binary.LittleEndian.Uint16(frame.Payload[4:]) != 0
		}
		if !request.wait {
			request.result <- result{}
		}
		return nil
	}
	defer func() {
		if !joystickActive && !speedActive {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var commands []pocket3.Command
		if joystickActive {
			commands = append(commands, pocket3.Neutral())
		}
		if speedActive {
			commands = append(commands, (pocket3.SpeedPayload{}).Command())
		}
		for _, command := range commands {
			send(sendRequest{ctx: stopCtx, command: command, result: make(chan result, 1)})
		}
	}()
	for {
		select {
		case <-c.stop:
			return
		case request := <-c.snapshots:
			state.Pending = len(pendingRequests) + len(pendingProperties)
			request.result <- state
		case request := <-c.properties:
			if err := request.ctx.Err(); err != nil {
				request.result <- propertyResult{err: err}
				continue
			}
			for {
				nextPropertyTransaction++
				if _, used := pendingProperties[nextPropertyTransaction]; !used {
					break
				}
			}
			command, _ := pocket3.SubscribeProperty(request.name, nextPropertyTransaction)
			pendingProperties[nextPropertyTransaction] = request
			if err := send(sendRequest{ctx: request.ctx, command: command, result: make(chan result, 1)}); err != nil {
				terminalErr = err
				return
			}
		case request := <-c.requests:
			if err := send(request); err != nil {
				terminalErr = err
				return
			}
		case packet := <-rx:
			if packet.err != nil {
				terminalErr = packet.err
				return
			}
			d, err := datalink.Parse(packet.data)
			if err != nil {
				state.Malformed++
				continue
			}
			if d.Session != c.cfg.SessionID {
				state.ForeignSession++
				continue
			}
			observe(&state, d)
			if d.Type == datalink.Media {
				publish(Event{PacketType: d.Type, Sequence: d.Sequence, Media: append([]byte(nil), d.Payload...)})
			}
			for _, frame := range duml.Scan(d.Payload) {
				if frame.Encryption() != 0 {
					publish(Event{PacketType: d.Type, Sequence: d.Sequence, Frame: &frame})
					continue
				}
				if frame.Sender == 0x28 && frame.Receiver == 2 && frame.CommandSet == 0 && frame.CommandID == 0x99 {
					if property, err := pocket3.ParsePropertyPush(frame.Payload); err == nil {
						if waiter, ok := pendingProperties[property.Transaction]; ok && waiter.name == property.Name {
							waiter.result <- propertyResult{property: property}
							delete(pendingProperties, property.Transaction)
						}
						for transaction, waiter := range pendingProperties {
							if waiter.observe && waiter.name == property.Name && waiter.ctx.Err() == nil {
								waiter.result <- propertyResult{property: property}
								delete(pendingProperties, transaction)
							}
						}
					}
				}
				if frame.IsResponse() && frame.Sender == 4 && frame.CommandSet == 4 && frame.CommandID == 0x50 && frame.Receiver == 2 && len(frame.Payload) > 0 {
					state.LastProbeReply = time.Now()
				}
				if p, ok := pendingRequests[frame.Sequence]; ok && frame.IsResponse() && frame.Sender == p.receiver && frame.Receiver == 2 && frame.CommandSet == p.set && frame.CommandID == p.id {
					response := frame
					response.Payload = append([]byte(nil), frame.Payload...)
					p.request.result <- result{frame: response}
					delete(pendingRequests, frame.Sequence)
				}
				frameCopy := frame
				publish(Event{PacketType: d.Type, Sequence: d.Sequence, Frame: &frameCopy})
			}
		case <-tick.C:
			for transaction, p := range pendingProperties {
				if err := p.ctx.Err(); err != nil {
					p.result <- propertyResult{err: err}
					delete(pendingProperties, transaction)
				}
			}
			for id, p := range pendingRequests {
				if err := p.request.ctx.Err(); err != nil {
					p.request.result <- result{err: fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)}
					delete(pendingRequests, id)
				}
			}
			if err := writeDatagram(c.conn, datalink.Datagram{Session: c.cfg.SessionID, Type: datalink.Acknowledgement, Payload: state.Window.MarshalBinary()}); err != nil {
				terminalErr = err
				return
			}
		case <-keepalive.C:
			for _, command := range []pocket3.Command{pocket3.AppPresence(), pocket3.GimbalProbe()} {
				request := sendRequest{ctx: context.Background(), command: command, result: make(chan result, 1)}
				if err := send(request); err != nil {
					terminalErr = err
					return
				}
			}
		}
	}
}
