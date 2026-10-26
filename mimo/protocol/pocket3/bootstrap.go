package pocket3

import (
	"encoding/hex"
	"errors"
	"openmimo/protocol/duml"
	"unicode/utf8"
)

const BLEService = "0000fff0-0000-1000-8000-00805f9b34fb"
const BLENotify = "0000fff4-0000-1000-8000-00805f9b34fb"
const BLEWrite = "0000fff5-0000-1000-8000-00805f9b34fb"

func PairArm() []byte { return []byte{1, 0} }

func PackString(value string) ([]byte, error) {
	if !utf8.ValidString(value) || len(value) > 255 {
		return nil, errors.New("DJI packed string requires valid UTF-8 and at most 255 bytes")
	}
	return append([]byte{byte(len(value))}, []byte(value)...), nil
}
func UnpackString(data []byte) (string, []byte, error) {
	if len(data) == 0 || int(data[0]) > len(data)-1 {
		return "", nil, errors.New("truncated DJI packed string")
	}
	end := 1 + int(data[0])
	if !utf8.Valid(data[1:end]) {
		return "", nil, errors.New("invalid DJI packed UTF-8")
	}
	return string(data[1:end]), data[end:], nil
}

func Pair(identifier, pin string) (Command, error) {
	if len(identifier) == 0 || len(identifier) > 32 {
		return Command{}, errors.New("pairing identifier must contain 1 to 32 UTF-8 bytes")
	}
	a, err := PackString(identifier)
	if err != nil {
		return Command{}, err
	}
	b, err := PackString(pin)
	if err != nil {
		return Command{}, err
	}
	return Command{Name: "pair", Receiver: 7, Set: 7, ID: 0x45, Flags: 0x40, Payload: append(a, b...)}, nil
}
func SessionWake() Command {
	return Command{Name: "ble-session-wake", Receiver: 0xf0, Set: 0, ID: 0x2b, Flags: 0x40, Payload: []byte{4, 0}}
}
func APWake() Command {
	return Command{Name: "wake-ap", Receiver: 0x1c, Set: 0x53, ID: 0x10, Flags: 0x40, Payload: []byte{0, 0, 0, 0}}
}
func QuerySSID() Command {
	return Command{Name: "query-ssid", Receiver: 7, Set: 7, ID: 0x07, Flags: 0x40}
}
func QueryPassword() Command {
	return Command{Name: "query-ap-password", Receiver: 7, Set: 7, ID: 0x0e, Flags: 0x40}
}

// PairingApprovalReply mirrors the request's addresses and sequence number.
func PairingApprovalReply(request duml.Frame) (duml.Frame, error) {
	if request.IsResponse() || request.CommandSet != 7 || request.CommandID != 0x46 {
		return duml.Frame{}, errors.New("not a pairing-approval request")
	}
	return duml.Frame{Sender: request.Receiver, Receiver: request.Sender, Sequence: request.Sequence, Flags: 0xc0, CommandSet: 7, CommandID: 0x46, Payload: append([]byte(nil), request.Payload...)}, nil
}

func HandshakePayload(base uint16) []byte {
	p, _ := hex.DecodeString("000064006400c005140000640000019001c005140000640014006400c00514000064000101040102")
	p[0] = byte(base)
	p[1] = byte(base >> 8)
	return p
}
func AppDeviceInfo() Command {
	p := make([]byte, 62)
	copy(p, []byte{0, 0x41, 0x50, 0x50})
	p[41] = 2
	p[50] = 2
	p[51] = 8
	return Command{Name: "app-device-info", Receiver: 0x48, Set: 0, ID: 0x81, Flags: 0x80, Payload: p}
}
func AppPresence() Command {
	return Command{Name: "app-presence", Receiver: 0x28, Set: 0, ID: 0x88, Flags: 0x40, Payload: []byte{0x17, 0, 0x46, 0x23, 0x7c, 0x41, 0x50, 0x50, 0, 0, 0, 0, 0, 2}}
}
