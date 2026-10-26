package mobilecore

import (
	"context"
	"encoding/hex"
	"errors"
	"openmimo/ble"
	"sync"
	"sync/atomic"
	"time"
)

// GATTLink carries platform operations through the JNI event channel. The
// Android adapter executes one operation at a time and acknowledges by ID.
type GATTLink struct {
	ID      uint64
	emit    func(context.Context, Event) error
	notify  chan ble.Notification
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	pending map[uint64]chan error
}

var nextGATTOperation atomic.Uint64
var nextGATTLink atomic.Uint64

func newGATT(emit func(context.Context, Event) error) *GATTLink {
	return &GATTLink{ID: nextGATTLink.Add(1), emit: emit, notify: make(chan ble.Notification, 128), done: make(chan struct{}), pending: make(map[uint64]chan error)}
}
func (g *GATTLink) Notifications() <-chan ble.Notification { return g.notify }
func (g *GATTLink) operation(ctx context.Context, op, uuid string, data []byte, without bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-g.done:
		return errors.New("GATT adapter closed")
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	id := nextGATTOperation.Add(1)
	result := make(chan error, 1)
	g.mu.Lock()
	g.pending[id] = result
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.pending, id); g.mu.Unlock() }()
	if err := g.emit(ctx, Event{Kind: "gatt-operation", Data: map[string]any{"link_id": g.ID, "id": id, "operation": op, "uuid": uuid, "data_hex": hex.EncodeToString(data), "without_response": without}}); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-g.done:
		return errors.New("GATT adapter closed")
	}
}
func (g *GATTLink) Subscribe(ctx context.Context, uuid string) error {
	return g.operation(ctx, "subscribe", uuid, nil, false)
}
func (g *GATTLink) Write(ctx context.Context, uuid string, data []byte, without bool) error {
	return g.operation(ctx, "write", uuid, data, without)
}
func (g *GATTLink) Ack(id uint64, message string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if waiter := g.pending[id]; waiter != nil {
		var err error
		if message != "" {
			err = errors.New(message)
		}
		select {
		case waiter <- err:
		default:
		}
	}
}
func (g *GATTLink) Notify(uuid string, data []byte, message string) error {
	n := ble.Notification{Characteristic: uuid, Data: append([]byte(nil), data...)}
	if message != "" {
		n.Err = errors.New(message)
	}
	select {
	case <-g.done:
		return errors.New("GATT adapter closed")
	case g.notify <- n:
		return nil
	default:
		g.Close()
		return errors.New("GATT notifications overflowed")
	}
}
func (g *GATTLink) Close() error { g.once.Do(func() { close(g.done) }); return nil }
