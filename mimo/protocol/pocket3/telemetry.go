package pocket3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"openmimo/protocol/duml"
)

type GimbalState struct {
	Pitch float64
	Roll  float64
	Yaw   float64
	Extra []byte
}

func ParseGimbal(payload []byte) (GimbalState, error) {
	if len(payload) < 6 {
		return GimbalState{}, errors.New("truncated 04/05 gimbal state")
	}
	return GimbalState{Pitch: float64(int16(binary.LittleEndian.Uint16(payload))) / 10, Roll: float64(int16(binary.LittleEndian.Uint16(payload[2:]))) / 10, Yaw: float64(int16(binary.LittleEndian.Uint16(payload[4:]))) / 10, Extra: append([]byte(nil), payload[6:]...)}, nil
}

type CameraDetails struct {
	VideoLike        bool
	StorageTotalMiB  uint32
	StorageFreeMiB   uint32
	RemainingSeconds uint16
	ElapsedSeconds   uint16
	Mode             Mode
}
type CameraState struct {
	Raw        byte
	Recording  bool
	Transition bool
	Details    *CameraDetails
}

func ParseCameraState(payload []byte) (CameraState, error) {
	if len(payload) < 1 {
		return CameraState{}, errors.New("truncated 02/80 camera state")
	}
	s := CameraState{Raw: payload[0], Recording: payload[0]&0x80 != 0, Transition: payload[0]&0x40 != 0}
	if len(payload) >= 58 {
		s.Details = &CameraDetails{VideoLike: payload[4] == 1, StorageTotalMiB: binary.LittleEndian.Uint32(payload[5:]), StorageFreeMiB: binary.LittleEndian.Uint32(payload[9:]), RemainingSeconds: binary.LittleEndian.Uint16(payload[17:]), ElapsedSeconds: binary.LittleEndian.Uint16(payload[29:]), Mode: Mode(payload[57])}
	}
	return s, nil
}

type BatteryState struct {
	Percent     byte
	Charging    *bool
	RawCharging byte
}

func ParseBattery(payload []byte) (BatteryState, error) {
	if len(payload) < 33 {
		return BatteryState{}, errors.New("truncated 0D/02 battery state")
	}
	if payload[20] > 100 {
		return BatteryState{}, errors.New("invalid battery percentage")
	}
	s := BatteryState{Percent: payload[20], RawCharging: payload[32]}
	if payload[32] <= 1 {
		charging := payload[32] == 1
		s.Charging = &charging
	}
	return s, nil
}

type StorageState struct {
	Count    byte
	TotalMiB uint32
	FreeMiB  uint32
}

func ParseStorage(payload []byte) (StorageState, error) {
	if len(payload) < 22 {
		return StorageState{}, errors.New("truncated 02/DC storage state")
	}
	return StorageState{Count: payload[2], TotalMiB: binary.LittleEndian.Uint32(payload[6:]), FreeMiB: binary.LittleEndian.Uint32(payload[10:])}, nil
}

// CheckAccepted checks replies whose documented first byte is a zero-success
// status. Do not use it for arbitrary DUML replies or as confirmation of a
// completed recording/gimbal action; observe the resulting state separately.
func CheckAccepted(frame duml.Frame) error {
	if !frame.IsResponse() {
		return errors.New("not a DUML response")
	}
	if len(frame.Payload) == 0 {
		return errors.New("response has no status byte")
	}
	if frame.Payload[0] != 0 {
		return fmt.Errorf("camera returned status 0x%02x", frame.Payload[0])
	}
	return nil
}

type PropertyPush struct {
	Transaction uint32
	Name        string
	Value       []byte
	Tail        []byte
}

func ascii(name string) bool {
	for _, b := range []byte(name) {
		if b > 127 || b == 0 {
			return false
		}
	}
	return true
}
func SubscribeProperty(name string, transaction uint32) (Command, error) {
	if !ascii(name) || len(name) == 0 || len(name) > duml.MaxFrameSize-duml.MinFrameSize-19 {
		return Command{}, errors.New("invalid or overlong property name")
	}
	p := make([]byte, 15+len(name)+4)
	p[0] = 2
	p[1] = 2
	binary.LittleEndian.PutUint32(p[4:], transaction)
	binary.LittleEndian.PutUint16(p[11:], uint16(len(name)+6))
	binary.LittleEndian.PutUint16(p[13:], uint16(len(name)))
	copy(p[15:], name)
	return Command{Name: "subscribe-property", Receiver: 0x28, Set: 0, ID: 0x99, Flags: 0x40, Payload: p}, nil
}
func ParsePropertyPush(payload []byte) (PropertyPush, error) {
	var p PropertyPush
	if len(payload) < 23 || payload[0] != 2 || payload[1] != 6 || payload[2] != 0 || payload[3] != 0 {
		return p, errors.New("invalid property push prefix")
	}
	n := int(binary.LittleEndian.Uint16(payload[13:]))
	nameEnd := 15 + n
	if n == 0 || nameEnd+8 > len(payload) {
		return p, errors.New("invalid property name length")
	}
	name := string(payload[15:nameEnd])
	if !ascii(name) {
		return p, errors.New("invalid property name")
	}
	size := int(binary.LittleEndian.Uint16(payload[nameEnd+6:]))
	start := nameEnd + 8
	if size > len(payload)-start {
		return p, errors.New("truncated property value")
	}
	p = PropertyPush{Transaction: binary.LittleEndian.Uint32(payload[4:]), Name: name, Value: append([]byte(nil), payload[start:start+size]...), Tail: append([]byte(nil), payload[start+size:]...)}
	return p, nil
}
