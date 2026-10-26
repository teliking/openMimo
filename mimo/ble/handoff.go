// Package ble implements Pocket 3's pairing/AP-credential protocol over a GATT
// adapter. It never joins an OS network or persists camera credentials.
package ble

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"sync"
	"time"
)

type Notification struct {
	Characteristic string
	Data           []byte
	Err            error
}

// Link must deliver copied notifications through a buffered channel. Writes
// complete or honor context cancellation; Close must unblock adapter operations.
type Link interface {
	Subscribe(context.Context, string) error
	Write(context.Context, string, []byte, bool) error
	Notifications() <-chan Notification
	Close() error
}
type Config struct {
	Identifier string
	PIN        string
	MaxWrite   int
	Step       time.Duration
	// Trace reports frame headers only; Wi-Fi credentials and pairing identifiers are excluded.
	Trace func(string)
}
type Phase string

const (
	Pairing          Phase = "pairing"
	AwaitingApproval Phase = "awaiting-camera-approval"
	WakingAP         Phase = "waking-ap"
	Ready            Phase = "credentials-ready"
)

type Credentials struct {
	SSID     string
	Password string `json:"-"`
}

func (c Credentials) String() string {
	return fmt.Sprintf("Credentials{SSID:%q, Password:<redacted>}", c.SSID)
}

type Client struct {
	link        Link
	cancel      context.CancelFunc
	done        chan struct{}
	ready       chan struct{}
	phases      chan Phase
	once        sync.Once
	linkOnce    sync.Once
	mu          sync.Mutex
	credentials Credentials
	err         error
	finished    bool
}

func Start(ctx context.Context, link Link, config Config) (*Client, error) {
	if link == nil {
		return nil, errors.New("nil GATT adapter")
	}
	if config.MaxWrite == 0 {
		config.MaxWrite = 20
	}
	if config.Step == 0 {
		config.Step = 100 * time.Millisecond
	}
	if config.PIN == "" {
		config.PIN = "osmo"
	}
	if config.MaxWrite < 1 || config.MaxWrite > 512 || config.Step < 0 || config.Step > time.Duration(1<<63-1)/300 {
		return nil, errors.New("invalid BLE handoff configuration")
	}
	if config.Identifier == "" {
		var identifier [16]byte
		if _, err := rand.Read(identifier[:]); err != nil {
			return nil, err
		}
		config.Identifier = hex.EncodeToString(identifier[:])
	}
	if _, err := pocket3.Pair(config.Identifier, config.PIN); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	c := &Client{link: link, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), phases: make(chan Phase, 8)}
	go c.run(lifetime, config)
	return c, nil
}
func (c *Client) Phases() <-chan Phase { return c.phases }
func (c *Client) Credentials(ctx context.Context) (Credentials, error) {
	select {
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case <-c.ready:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.credentials, c.err
	}
}
func (c *Client) Close() error {
	c.once.Do(func() { c.cancel(); c.closeLink() })
	<-c.done
	return nil
}
func (c *Client) closeLink() { c.linkOnce.Do(func() { c.link.Close() }) }
func (c *Client) finish(credentials Credentials, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	c.credentials = credentials
	c.err = err
	c.finished = true
	close(c.ready)
}

func (c *Client) run(ctx context.Context, cfg Config) {
	defer close(c.done)
	defer close(c.phases)
	defer c.closeLink()
	defer c.finish(Credentials{}, errors.New("BLE handoff closed before credentials arrived"))
	phase := func(p Phase) {
		select {
		case c.phases <- p:
		default:
		}
	}
	fail := func(err error) { c.finish(Credentials{}, err) }
	writeFrame := func(frame duml.Frame) error {
		if cfg.Trace != nil && (frame.CommandSet == 7 || frame.CommandSet == 0x53) {
			cfg.Trace(fmt.Sprintf("TX %02x→%02x %02x/%02x flags=%02x seq=%04x bytes=%d", frame.Sender, frame.Receiver, frame.CommandSet, frame.CommandID, frame.Flags, frame.Sequence, len(frame.Payload)))
		}
		data, err := frame.MarshalBinary()
		if err != nil {
			return err
		}
		for len(data) > 0 {
			n := cfg.MaxWrite
			if n > len(data) {
				n = len(data)
			}
			if err = c.link.Write(ctx, pocket3.BLEWrite, append([]byte(nil), data[:n]...), true); err != nil {
				return err
			}
			data = data[n:]
		}
		return nil
	}
	send := func(command pocket3.Command, sequence uint16) error { return writeFrame(command.Frame(sequence)) }
	for _, characteristic := range []string{pocket3.BLENotify, pocket3.BLEWrite} {
		if err := c.link.Subscribe(ctx, characteristic); err != nil {
			fail(err)
			return
		}
	}
	if err := c.link.Write(ctx, pocket3.BLENotify, pocket3.PairArm(), false); err != nil {
		fail(err)
		return
	}
	if err := send(pocket3.SessionWake(), 0x802b); err != nil {
		fail(err)
		return
	}
	phase(Pairing)
	started := time.Now()
	pairDue := started.Add(cfg.Step + cfg.Step/5)
	retryDue := started.Add(25 * cfg.Step)
	pairDeadline := started.Add(300 * cfg.Step)
	var pairedAt time.Time
	var credentials Credentials
	pairSent, pairStatus, apSent, ssidSent, passwordSent := false, false, false, false, false
	retries := 0
	tickDuration := cfg.Step / 5
	if tickDuration < time.Millisecond {
		tickDuration = time.Millisecond
	}
	ticker := time.NewTicker(tickDuration)
	defer ticker.Stop()
	keepalive := time.NewTicker(10 * cfg.Step)
	defer keepalive.Stop()
	streams := map[string]*duml.Stream{pocket3.BLENotify: {}, pocket3.BLEWrite: {}}
	traceChunks := 0
	paired := func() {
		if pairedAt.IsZero() {
			pairedAt = time.Now()
			phase(WakingAP)
		}
	}
	for {
		select {
		case <-ctx.Done():
			fail(ctx.Err())
			return
		case notification, open := <-c.link.Notifications():
			if !open {
				fail(errors.New("BLE notification channel closed"))
				return
			}
			if notification.Err != nil {
				fail(notification.Err)
				return
			}
			stream := streams[notification.Characteristic]
			if stream == nil {
				continue
			}
			if cfg.Trace != nil && traceChunks < 70 {
				traceChunks++
				b := notification.Data
				if len(b) >= 11 && b[0] == 0x55 && duml.CRC8(b[:3]) == b[3] {
					cfg.Trace(fmt.Sprintf("chunk %s size=%d header=%x", notification.Characteristic[4:8], len(b), b[:11]))
				}
			}
			discarded := stream.Discarded
			frames := stream.Feed(notification.Data)
			if cfg.Trace != nil && traceChunks < 70 && stream.Discarded != discarded {
				cfg.Trace(fmt.Sprintf("RX framing discarded=%d buffered=%d chunk=%d", stream.Discarded-discarded, stream.Buffered(), len(notification.Data)))
			}
			for _, frame := range frames {
				if cfg.Trace != nil && (frame.CommandSet == 7 || frame.CommandSet == 0x53 || (!frame.IsResponse() && frame.AckType() != 0)) {
					cfg.Trace(fmt.Sprintf("RX %02x→%02x %02x/%02x flags=%02x seq=%04x bytes=%d", frame.Sender, frame.Receiver, frame.CommandSet, frame.CommandID, frame.Flags, frame.Sequence, len(frame.Payload)))
					if frame.IsResponse() && frame.CommandSet == 0x53 && len(frame.Payload) == 1 {
						cfg.Trace(fmt.Sprintf("AP wake status=%02x", frame.Payload[0]))
					}
				}
				if frame.Receiver != 2 {
					continue
				}
				if frame.Encryption() != 0 {
					fail(errors.New("encrypted BLE DUML variant requires further analysis"))
					return
				}
				if !frame.IsResponse() && frame.AckType() != 0 {
					if frame.CommandSet == 7 && frame.CommandID == 0x46 {
						response, err := pocket3.PairingApprovalReply(frame)
						if err != nil {
							fail(err)
							return
						}
						if err = writeFrame(response); err != nil {
							fail(err)
							return
						}
						if len(frame.Payload) == 0 || frame.Payload[0] != 1 {
							fail(errors.New("camera pairing approval was not granted"))
							return
						}
						paired()
					} else if frame.CommandSet == 0 && frame.CommandID == 0x81 {
						response := frame
						response.Sender, response.Receiver = frame.Receiver, frame.Sender
						response.Flags = 0xc0
						response.Payload = pocket3.AppDeviceInfo().Payload
						if err := writeFrame(response); err != nil {
							fail(err)
							return
						}
					}
					continue
				}
				if frame.CommandSet != 7 || frame.Sender != 7 || !frame.IsResponse() {
					continue
				}
				switch frame.CommandID {
				case 0x45:
					if len(frame.Payload) < 2 || frame.Payload[0] != 0 {
						fail(fmt.Errorf("invalid pairing status reply (payload=%x)", frame.Payload))
						return
					}
					pairStatus = true
					switch frame.Payload[1] {
					case 1:
						paired()
					case 2:
						phase(AwaitingApproval)
					default:
						fail(fmt.Errorf("unmapped camera pairing status 0x%02x", frame.Payload[1]))
						return
					}
				case 0x46:
					if len(frame.Payload) > 0 && frame.Payload[0] == 1 {
						paired()
					}
				case 7, 0x0e:
					if pairedAt.IsZero() {
						continue
					}
					if len(frame.Payload) < 2 || frame.Payload[0] != 0 {
						fail(errors.New("camera credential query failed"))
						return
					}
					value, _, err := pocket3.UnpackString(frame.Payload[1:])
					if err != nil || value == "" {
						fail(errors.New("invalid camera credential reply"))
						return
					}
					if frame.CommandID == 7 {
						credentials.SSID = value
					} else {
						credentials.Password = value
					}
					if credentials.SSID != "" && credentials.Password != "" {
						c.finish(credentials, nil)
						phase(Ready)
					}
				}
			}
		case now := <-ticker.C:
			if pairedAt.IsZero() {
				if now.After(pairDeadline) {
					fail(errors.New("camera pairing timed out"))
					return
				}
				if !pairSent && !now.Before(pairDue) {
					command, _ := pocket3.Pair(cfg.Identifier, cfg.PIN)
					if err := send(command, 0x8092); err != nil {
						fail(err)
						return
					}
					pairSent = true
				}
				if pairSent && !pairStatus && retries < 2 && !now.Before(retryDue) {
					command, _ := pocket3.Pair(cfg.Identifier, cfg.PIN)
					if err := send(command, 0x8092); err != nil {
						fail(err)
						return
					}
					retries++
					retryDue = retryDue.Add(25 * cfg.Step)
				}
				continue
			}
			elapsed := now.Sub(pairedAt)
			if !apSent && elapsed >= cfg.Step {
				if err := send(pocket3.APWake(), 0x8053); err != nil {
					fail(err)
					return
				}
				apSent = true
			}
			if !ssidSent && elapsed >= 9*cfg.Step {
				if err := send(pocket3.QuerySSID(), 0x8007); err != nil {
					fail(err)
					return
				}
				ssidSent = true
			}
			if !passwordSent && elapsed >= 14*cfg.Step {
				if err := send(pocket3.QueryPassword(), 0x800e); err != nil {
					fail(err)
					return
				}
				passwordSent = true
			}
			if elapsed > 65*cfg.Step && (credentials.SSID == "" || credentials.Password == "") {
				fail(errors.New("paired camera did not return Wi-Fi credentials"))
				return
			}
		case <-keepalive.C:
			if !pairedAt.IsZero() {
				command := pocket3.SessionWake()
				command.Name = "ble-keepalive"
				command.Payload = []byte{1, 1}
				if err := send(command, 0x802b); err != nil {
					fail(err)
					return
				}
			}
		}
	}
}
