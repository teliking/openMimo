// Package mobilecore is a platform-independent app engine shared by Android JNI
// and host tests. OS radios are adapters, protocol/control state stays in Go.
package mobilecore

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"openmimo/ble"
	"openmimo/control"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"openmimo/sequencer"
	"openmimo/session"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Event struct {
	Kind string `json:"kind"`
	Data any    `json:"data,omitempty"`
}
type response struct {
	OK             bool   `json:"ok"`
	Data           any    `json:"data,omitempty"`
	Error          string `json:"error,omitempty"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}
type request struct {
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

type cameraSession interface {
	Send(context.Context, pocket3.Command) error
	Request(context.Context, pocket3.Command) (duml.Frame, error)
	ReadProperty(context.Context, string) (pocket3.PropertyPush, error)
	ObserveProperty(context.Context, string) (pocket3.PropertyPush, error)
	Stats(context.Context) (session.Stats, error)
	Events() <-chan session.Event
	Err() error
	Close() error
}

type Engine struct {
	bleLifecycle     sync.Mutex
	networkHandle    atomic.Uint64
	socketBinder     func(uint64, syscall.RawConn) error
	mu               sync.Mutex
	events           chan Event
	client           cameraSession
	connectCancel    context.CancelFunc
	generation       uint64
	connection       string
	address          string
	lastError        string
	properties       map[string]any
	jogPitch, jogYaw int
	jogUntil         time.Time
	scriptCancel     context.CancelFunc
	scriptID         uint64
	scriptState      string
	bleClient        *ble.Client
	bleCancel        context.CancelFunc
	gatt             *GATTLink
	bleGeneration    uint64
}

func New() *Engine {
	return &Engine{events: make(chan Event, 256), connection: "disconnected", scriptState: "idle", properties: make(map[string]any)}
}
func (e *Engine) SetSocketBinder(binder func(uint64, syscall.RawConn) error) {
	e.mu.Lock()
	e.socketBinder = binder
	e.mu.Unlock()
}
func (e *Engine) emit(kind string, data any) {
	select {
	case e.events <- Event{kind, data}:
	default:
	}
}
func (e *Engine) critical(ctx context.Context, event Event) error {
	select {
	case e.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func decode(data []byte, v any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// Dispatch always returns a JSON envelope. JNI calls can be concurrent, and no
// JNIEnv or Java object is retained by the Go engine.
func (e *Engine) Dispatch(data []byte) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			out, _ = json.Marshal(response{Error: fmt.Sprintf("engine recovered: %v", r)})
		}
	}()
	var req request
	if len(data) > 1<<20 {
		out, _ = json.Marshal(response{Error: "request exceeds 1 MiB"})
		return
	}
	if err := decode(data, &req); err != nil {
		out, _ = json.Marshal(response{Error: err.Error()})
		return
	}
	value, err := e.dispatch(req)
	r := response{OK: err == nil, Data: value}
	if err != nil {
		r.Error = err.Error()
		r.OutcomeUnknown = errors.Is(err, session.ErrOutcomeUnknown)
	}
	out, marshalErr := json.Marshal(r)
	if marshalErr != nil {
		out, _ = json.Marshal(response{Error: marshalErr.Error()})
	}
	return
}
func (e *Engine) dispatch(req request) (any, error) {
	switch req.Op {
	case "network":
		var p struct {
			Handle uint64 `json:"handle"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		e.networkHandle.Store(p.Handle)
		return nil, nil
	case "network-check":
		var p struct {
			Handle uint64 `json:"handle"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		e.mu.Lock()
		binder := e.socketBinder
		e.mu.Unlock()
		if binder == nil {
			return nil, errors.New("platform socket binder unavailable")
		}
		connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return nil, err
		}
		defer connection.Close()
		raw, err := connection.SyscallConn()
		if err != nil {
			return nil, err
		}
		if err = binder(p.Handle, raw); err != nil {
			return nil, err
		}
		return map[string]any{"bound": true, "packets_sent": 0}, nil
	case "poll":
		events := []Event{}
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case v := <-e.events:
			events = append(events, v)
		case <-timer.C:
			return events, nil
		}
		for len(events) < 32 {
			select {
			case v := <-e.events:
				events = append(events, v)
			default:
				return events, nil
			}
		}
		return events, nil
	case "status":
		e.mu.Lock()
		defer e.mu.Unlock()
		properties := make(map[string]any, len(e.properties))
		for k, v := range e.properties {
			properties[k] = v
		}
		return map[string]any{"connection": e.connection, "address": e.address, "last_error": e.lastError, "script": e.scriptState, "properties": properties, "target": "Pocket 3 / HG212", "hardware_validation": "pending"}, nil
	case "connect":
		var p struct {
			Address string `json:"address"`
			SkipTCP bool   `json:"skip_tcp"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if p.Address == "" {
			p.Address = "192.168.2.1:9004"
		}
		if _, _, err := net.SplitHostPort(p.Address); err != nil {
			return nil, err
		}
		return nil, e.connect(p.Address, p.SkipTCP)
	case "disconnect":
		e.disconnect()
		e.stopBLE()
		return nil, nil
	case "action":
		var a control.Action
		if err := decode(req.Params, &a); err != nil {
			return nil, err
		}
		e.mu.Lock()
		busy := e.scriptCancel != nil
		e.mu.Unlock()
		if busy && a.Type != "record-stop" && a.Type != "gimbal-stop" {
			return nil, errors.New("script is running; cancel it before manual control")
		}
		return e.execute(context.Background(), a)
	case "jog":
		var p struct {
			Pitch int `json:"pitch"`
			Yaw   int `json:"yaw"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		e.mu.Lock()
		busy := e.scriptCancel != nil
		e.mu.Unlock()
		if busy && (p.Pitch != 0 || p.Yaw != 0) {
			return nil, errors.New("script is running")
		}
		return nil, e.jog(p.Pitch, p.Yaw)
	case "read-property", "observe-property":
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		c := e.camera()
		if c == nil {
			return nil, errors.New("camera is disconnected")
		}
		var property pocket3.PropertyPush
		var err error
		if req.Op == "observe-property" {
			property, err = c.ObserveProperty(context.Background(), p.Name)
		} else {
			property, err = c.ReadProperty(context.Background(), p.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		base := e.property(c, property).(map[string]any)
		value := make(map[string]any, len(base)+3)
		for key, item := range base {
			value[key] = item
		}
		if req.Op == "observe-property" {
			value["source"] = "live-push"
			value["request_correlated"] = false
			value["received_at"] = time.Now().UTC()
		}
		return value, nil
	case "script-validate", "script-start":
		var p sequencer.Plan
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if req.Op == "script-validate" {
			return map[string]any{"valid": true, "total_steps": len(p.Steps) * p.Repeat}, nil
		}
		return nil, e.startScript(p)
	case "script-stop":
		e.cancelScript()
		return nil, nil
	case "self-test":
		return selfTest()
	case "ble-start":
		var p struct {
			Identifier string `json:"identifier"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if err := e.startBLE(p.Identifier); err != nil {
			return nil, err
		}
		e.mu.Lock()
		if e.gatt == nil {
			e.mu.Unlock()
			return nil, errors.New("BLE start was cancelled")
		}
		id := e.gatt.ID
		e.mu.Unlock()
		return map[string]any{"link_id": id}, nil
	case "ble-stop":
		e.stopBLE()
		return nil, nil
	case "ble-ack":
		var p struct {
			ID    uint64 `json:"id"`
			Error string `json:"error"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		e.mu.Lock()
		g := e.gatt
		e.mu.Unlock()
		if g != nil {
			g.Ack(p.ID, p.Error)
		}
		return nil, nil
	case "ble-notify":
		var p struct {
			LinkID uint64 `json:"link_id"`
			UUID   string `json:"uuid"`
			Data   string `json:"data_hex"`
			Error  string `json:"error"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		data, err := hex.DecodeString(p.Data)
		if err != nil {
			return nil, err
		}
		e.mu.Lock()
		g := e.gatt
		e.mu.Unlock()
		if g == nil || g.ID != p.LinkID {
			return nil, errors.New("BLE protocol is stopped")
		}
		return nil, g.Notify(p.UUID, data, p.Error)
	default:
		return nil, fmt.Errorf("unknown operation %q", req.Op)
	}
}
func (e *Engine) camera() cameraSession { e.mu.Lock(); defer e.mu.Unlock(); return e.client }
func (e *Engine) connect(address string, skipTCP bool) error {
	e.mu.Lock()
	if e.client != nil {
		e.mu.Unlock()
		return errors.New("disconnect the current session first")
	}
	if e.connectCancel != nil {
		e.connectCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.connectCancel = cancel
	e.generation++
	generation := e.generation
	e.connection, e.address, e.lastError = "connecting", address, ""
	binder, handle := e.socketBinder, e.networkHandle.Load()
	e.mu.Unlock()
	e.emit("connection", "connecting")
	go func() {
		setup, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		cfg := session.Config{Address: address, SkipTCPBootstrap: skipTCP, RequestTimeout: 3 * time.Second}
		if binder != nil {
			cfg.SocketControl = func(_, _ string, raw syscall.RawConn) error { return binder(handle, raw) }
		}
		c, err := session.Dial(setup, cfg)
		e.mu.Lock()
		if generation != e.generation || ctx.Err() != nil {
			e.mu.Unlock()
			if c != nil {
				c.Close()
			}
			return
		}
		if err != nil {
			e.connection = "disconnected"
			e.lastError = err.Error()
			e.mu.Unlock()
			e.emit("error", err.Error())
			e.emit("connection", "disconnected")
			return
		}
		e.client = c
		e.connection = "connected"
		e.mu.Unlock()
		e.emit("connection", "connected")
		go e.jogLoop(ctx, c)
		go e.metrics(ctx, c)
		e.receive(ctx, c, generation)
	}()
	return nil
}
func (e *Engine) disconnect() {
	e.cancelScript()
	e.mu.Lock()
	e.generation++
	cancel := e.connectCancel
	e.connectCancel = nil
	c := e.client
	e.client = nil
	e.connection = "disconnected"
	e.jogUntil = time.Time{}
	e.properties = make(map[string]any)
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		c.Close()
	}
	e.emit("connection", "disconnected")
}
func (e *Engine) receive(ctx context.Context, c cameraSession, generation uint64) {
	for _, name := range pocket3.CoreProperties() {
		command, _ := pocket3.SubscribeProperty(name, uint32(1+len(name)))
		if err := c.Send(ctx, command); err != nil {
			e.emit("error", err.Error())
			break
		}
	}
	for event := range c.Events() {
		if ctx.Err() != nil {
			break
		}
		if event.Frame == nil || event.Frame.Encryption() != 0 {
			continue
		}
		f := event.Frame
		var kind string
		var value any
		var err error
		switch {
		case f.CommandSet == 4 && f.CommandID == 5:
			kind = "gimbal"
			value, err = pocket3.ParseGimbal(f.Payload)
		case f.CommandSet == 2 && f.CommandID == 0x80:
			kind = "camera"
			value, err = pocket3.ParseCameraState(f.Payload)
		case f.CommandSet == 13 && f.CommandID == 2:
			kind = "battery"
			value, err = pocket3.ParseBattery(f.Payload)
		case f.CommandSet == 2 && f.CommandID == 0xdc:
			kind = "storage"
			value, err = pocket3.ParseStorage(f.Payload)
		case f.CommandSet == 0 && f.CommandID == 0x99:
			var p pocket3.PropertyPush
			p, err = pocket3.ParsePropertyPush(f.Payload)
			if err == nil {
				kind = "property"
				value = e.property(c, p)
			}
		}
		if kind != "" && err == nil {
			e.emit(kind, value)
		}
	}
	wasCancelled := ctx.Err() != nil
	e.mu.Lock()
	current := generation == e.generation
	if current {
		e.client = nil
		e.connection = "disconnected"
		e.properties = make(map[string]any)
		if c.Err() != nil {
			e.lastError = c.Err().Error()
		}
		if e.connectCancel != nil {
			e.connectCancel()
		}
	}
	e.mu.Unlock()
	if current && !wasCancelled {
		e.emit("error", fmt.Sprint(c.Err()))
	}
	if current {
		e.emit("connection", "disconnected")
	}
}
func (e *Engine) metrics(ctx context.Context, c cameraSession) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			query, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			s, err := c.Stats(query)
			cancel()
			if err != nil {
				return
			}
			e.emit("link", map[string]any{"healthy": s.Healthy(time.Now()), "stats": s})
		}
	}
}
func (e *Engine) property(c cameraSession, p pocket3.PropertyPush) any {
	v := map[string]any{"name": p.Name, "value_hex": hex.EncodeToString(p.Value), "transaction": p.Transaction}
	if decoded, err := pocket3.DecodeProperty(p.Name, p.Value); err == nil {
		v["decoded"] = decoded
	} else if !errors.Is(err, pocket3.ErrUnknownProperty) {
		v["decode_error"] = err.Error()
	}
	e.mu.Lock()
	if e.client == c {
		e.properties[p.Name] = v
	}
	e.mu.Unlock()
	return v
}
func (e *Engine) execute(ctx context.Context, a control.Action) (any, error) {
	command, err := a.Command()
	if err != nil {
		return nil, err
	}
	c := e.camera()
	if c == nil {
		return nil, errors.New("camera is disconnected")
	}
	if a.Type == "gimbal-stop" {
		e.jog(0, 0)
	}
	if a.Type == "gimbal-jog" {
		defer e.jog(0, 0)
		timer := time.NewTimer(time.Duration(a.DurationMS) * time.Millisecond)
		defer timer.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if err := e.jog(a.Pitch, a.Yaw); err != nil {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return map[string]any{"sent": true}, nil
			case <-ticker.C:
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if command.Flags&0x60 == 0 {
		return map[string]any{"sent": true}, c.Send(ctx, command)
	}
	reply, err := c.Request(ctx, command)
	if err != nil {
		return nil, err
	}
	if command.Set == 2 {
		if err := pocket3.CheckAccepted(reply); err != nil {
			return nil, fmt.Errorf("%s (%02x/%02x): %w", a.Type, command.Set, command.ID, err)
		}
	}
	e.emit("action", a.Type)
	return map[string]any{"response_hex": hex.EncodeToString(reply.Payload), "action": a.Type}, nil
}
func (e *Engine) jog(pitch, yaw int) error {
	if _, err := pocket3.Joystick(pitch, yaw); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client == nil {
		return errors.New("camera is disconnected")
	}
	e.jogPitch, e.jogYaw = pitch, yaw
	e.jogUntil = time.Now().Add(250 * time.Millisecond)
	return nil
}
func (e *Engine) jogLoop(ctx context.Context, c cameraSession) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	active := false
	stop := func() {
		timeout, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		c.Send(timeout, pocket3.Neutral())
	}
	defer func() {
		if active {
			stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.mu.Lock()
			p, y, until := e.jogPitch, e.jogYaw, e.jogUntil
			e.mu.Unlock()
			if time.Now().Before(until) && (p != 0 || y != 0) {
				cmd, _ := pocket3.Joystick(p, y)
				timeout, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
				err := c.Send(timeout, cmd)
				cancel()
				if err != nil {
					return
				}
				active = true
			} else if active {
				stop()
				active = false
			}
		}
	}
}
func (e *Engine) startScript(p sequencer.Plan) error {
	e.mu.Lock()
	if e.client == nil {
		e.mu.Unlock()
		return errors.New("camera is disconnected")
	}
	if e.scriptCancel != nil {
		e.mu.Unlock()
		return errors.New("a script is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.scriptCancel = cancel
	e.scriptID++
	id := e.scriptID
	e.scriptState = "running"
	e.mu.Unlock()
	e.emit("script-state", "running")
	go func() {
		err := sequencer.Run(ctx, p, func(ctx context.Context, a control.Action) error { _, err := e.execute(ctx, a); return err }, func(v sequencer.Progress) { e.emit("script-progress", v) })
		e.jog(0, 0)
		state := "completed"
		if errors.Is(err, context.Canceled) {
			state = "cancelled"
		} else if err != nil {
			state = "failed"
			e.emit("error", err.Error())
		}
		e.mu.Lock()
		if id == e.scriptID {
			e.scriptCancel = nil
			e.scriptState = state
		}
		e.mu.Unlock()
		cancel()
		e.emit("script-state", state)
	}()
	return nil
}
func (e *Engine) cancelScript() {
	e.mu.Lock()
	cancel := e.scriptCancel
	e.jogUntil = time.Time{}
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (e *Engine) startBLE(identifier string) error {
	e.bleLifecycle.Lock()
	defer e.bleLifecycle.Unlock()
	e.stopBLELocked()
	ctx, cancel := context.WithCancel(context.Background())
	g := newGATT(e.critical)
	client, err := ble.Start(ctx, g, ble.Config{Identifier: identifier, Trace: func(message string) {
		e.emit("ble-trace", message)
	}})
	if err != nil {
		cancel()
		return err
	}
	e.mu.Lock()
	e.bleGeneration++
	generation := e.bleGeneration
	e.bleClient, e.bleCancel, e.gatt = client, cancel, g
	e.mu.Unlock()
	go func() {
		for phase := range client.Phases() {
			e.emit("ble-phase", map[string]any{"link_id": g.ID, "phase": phase})
		}
	}()
	go func() {
		credentials, err := client.Credentials(ctx)
		if err != nil {
			if ctx.Err() == nil {
				e.emit("ble-error", map[string]any{"link_id": g.ID, "error": err.Error()})
			}
			return
		}
		e.mu.Lock()
		current := generation == e.bleGeneration
		e.mu.Unlock()
		if current {
			e.critical(ctx, Event{Kind: "credentials", Data: map[string]any{"link_id": g.ID, "ssid": credentials.SSID, "password": credentials.Password}})
		}
	}()
	return nil
}
func (e *Engine) stopBLE() {
	e.bleLifecycle.Lock()
	defer e.bleLifecycle.Unlock()
	e.stopBLELocked()
}
func (e *Engine) stopBLELocked() {
	e.mu.Lock()
	e.bleGeneration++
	c, cancel, g := e.bleClient, e.bleCancel, e.gatt
	e.bleClient, e.bleCancel, e.gatt = nil, nil, nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if g != nil {
		g.Close()
	}
	if c != nil {
		c.Close()
	}
}
func selfTest() (any, error) {
	raw, _ := hex.DecodeString("550f04a202f02b8040002b04002440")
	frame, err := duml.Parse(raw)
	cmd, formatErr := (pocket3.VideoSettings{Resolution: 16, FrameRate: 7, SpeedRatio: 4}).Command()
	v, v2Err := pocket3.ParseVideoParametersV2([]byte{16, 6, 0, 0, 0, 2, 1, 0, 0xb1})
	checks := map[string]bool{"duml_crc": err == nil && frame.CommandID == 0x2b, "hg212_format": formatErr == nil && hex.EncodeToString(cmd.Payload) == "1007000400", "resolution_mapping": pocket3.SDKResolutionToWire(98) == 101, "video_v2": v2Err == nil && v.Compression == 1, "invalid_iso": false, "script_validation": false}
	_, err = pocket3.SetManualISO(123)
	checks["invalid_iso"] = err != nil
	err = (sequencer.Plan{Version: 1, Name: "offline", Repeat: 1, Steps: []sequencer.Step{{Action: &control.Action{Type: "photo"}}, {WaitMS: 100}}}).Validate()
	checks["script_validation"] = err == nil
	for name, ok := range checks {
		if !ok {
			return checks, fmt.Errorf("self-test failed: %s", name)
		}
	}
	return checks, nil
}
