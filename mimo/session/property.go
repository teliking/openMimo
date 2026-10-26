package session

import (
	"context"
	"openmimo/protocol/pocket3"
)

type propertyResult struct {
	property pocket3.PropertyPush
	err      error
}
type propertyRequest struct {
	ctx     context.Context
	name    string
	result  chan propertyResult
	observe bool
}

// ReadProperty installs a waiter before sending its 00/99 subscription. Only a
// push matching name AND transaction completes it; Events retains the raw frame.
// Firmware transaction echo semantics still require hardware validation. This
// does not silently fall back to potentially stale pushes from other requests.
// The camera subscription may persist until the session ends.
func (c *Client) ReadProperty(ctx context.Context, name string) (pocket3.PropertyPush, error) {
	return c.readProperty(ctx, name, false)
}

// ObserveProperty subscribes, then waits for the next received matching push.
// It does not claim request/response correlation: HG212 assigns push counters
// independently. No cached value completes this operation.
func (c *Client) ObserveProperty(ctx context.Context, name string) (pocket3.PropertyPush, error) {
	return c.readProperty(ctx, name, true)
}
func (c *Client) readProperty(ctx context.Context, name string, observe bool) (pocket3.PropertyPush, error) {
	if _, err := pocket3.SubscribeProperty(name, 1); err != nil {
		return pocket3.PropertyPush{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	request := propertyRequest{ctx: waitCtx, name: name, result: make(chan propertyResult, 1), observe: observe}
	select {
	case <-waitCtx.Done():
		return pocket3.PropertyPush{}, waitCtx.Err()
	case <-c.done:
		return pocket3.PropertyPush{}, c.Err()
	case c.properties <- request:
	}
	select {
	case result := <-request.result:
		return result.property, result.err
	case <-waitCtx.Done():
		return pocket3.PropertyPush{}, waitCtx.Err()
	case <-c.done:
		select {
		case result := <-request.result:
			return result.property, result.err
		default:
			return pocket3.PropertyPush{}, c.Err()
		}
	}
}
