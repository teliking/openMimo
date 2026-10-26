// Package pocket3 encodes Pocket 3 commands and decodes observed state messages.
// Hardware acceptance must be checked separately from command serialization.
package pocket3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"openmimo/protocol/duml"
)

type Command struct {
	Name     string
	Receiver byte
	Set      byte
	ID       byte
	Flags    byte
	Payload  []byte
}

func (c Command) Frame(sequence uint16) duml.Frame {
	return duml.Frame{Sender: 2, Receiver: c.Receiver, Sequence: sequence, Flags: c.Flags, CommandSet: c.Set, CommandID: c.ID, Payload: append([]byte(nil), c.Payload...)}
}
func camera(name string, id byte, payload ...byte) Command {
	return Command{Name: name, Receiver: 1, Set: 2, ID: id, Flags: 0x40, Payload: payload}
}
func Record(start bool) Command {
	var value byte
	if start {
		value = 1
	}
	return camera("record", 2, value)
}
func ShootPhoto() Command    { return camera("shoot-photo", 1, 1) }
func ShootPanorama() Command { return camera("shoot-panorama", 1, 7) }

type Mode byte

const (
	SlowMotion  Mode = 0
	Video       Mode = 1
	Timelapse   Mode = 2
	Photo       Mode = 5
	Hyperlapse  Mode = 10
	Panorama    Mode = 12
	Motionlapse Mode = 24
	LowLight    Mode = 40
)

func SetMode(mode Mode) (Command, error) {
	switch mode {
	case SlowMotion, Video, Timelapse, Photo, Hyperlapse, Panorama, Motionlapse, LowLight:
		return camera("camera-mode", 0xe1, byte(mode)), nil
	}
	return Command{}, fmt.Errorf("unmapped Pocket 3 shooting mode %d", mode)
}

// VideoFormat preserves all five observed bytes. Mimo preserves opaque fields
// when setting resolution, frame rate, or the low six speed-ratio bits.
type VideoFormat [5]byte

func ParseVideoFormat(data []byte) (VideoFormat, error) {
	var v VideoFormat
	if len(data) != 5 {
		return v, errors.New("video format requires five bytes")
	}
	copy(v[:], data)
	return v, nil
}
func (v VideoFormat) WithFormat(resolution, frameRate byte) VideoFormat {
	v[0] = resolution
	v[1] = frameRate
	return v
}
func (v VideoFormat) WithSpeedRatio(ratio byte) (VideoFormat, error) {
	if ratio > 63 {
		return v, errors.New("video speed ratio exceeds six bits")
	}
	v[3] = (v[3] & 0xc0) | ratio
	return v, nil
}
func (v VideoFormat) Command() Command {
	return camera("video-format", 0x18, append([]byte(nil), v[:]...)...)
}

func SetCompression(hevc bool) Command {
	var value byte
	if hevc {
		value = 1
	}
	return camera("video-compression", 0xab, value, 0)
}
func SetExposureMode(manual bool) Command {
	value := byte(1)
	if manual {
		value = 4
	}
	return camera("exposure-mode", 0x1e, value, 0)
}
func SetManualISO(iso int) (Command, error) {
	values := map[int]byte{0: 0, 50: 2, 100: 3, 200: 4, 400: 5, 800: 6, 1600: 7, 3200: 8, 6400: 9}
	code, ok := values[iso]
	if !ok {
		return Command{}, fmt.Errorf("unmapped manual ISO %d", iso)
	}
	return camera("manual-iso", 0x2a, code), nil
}
func SetISOMax(iso int) (Command, error) {
	values := map[int]byte{100: 1, 200: 2, 400: 3, 800: 4, 1600: 5, 3200: 6, 6400: 7}
	code, ok := values[iso]
	if !ok {
		return Command{}, fmt.Errorf("unmapped ISO MAX %d", iso)
	}
	return SetProperty(15, []byte{code})
}
func SetEV(thirds int) (Command, error) {
	if thirds < -9 || thirds > 9 {
		return Command{}, errors.New("EV outside -3..3 stops")
	}
	return camera("exposure-compensation", 0x2e, byte(16+thirds)), nil
}
func WhiteBalanceAuto() Command { return camera("white-balance-auto", 0x2c, 0, 0, 0, 0, 0) }
func WhiteBalanceKelvin(kelvin int) (Command, error) {
	if kelvin < 2000 || kelvin > 10000 || kelvin%100 != 0 {
		return Command{}, errors.New("white balance requires 2000..10000 K in 100 K steps")
	}
	return camera("white-balance-kelvin", 0x2c, 6, byte(kelvin/100), 0, 0, 0), nil
}
func SetColorProfile(profile byte) (Command, error) {
	if profile != 0 && profile != 0x3c && profile != 0x3d {
		return Command{}, errors.New("unmapped color profile")
	}
	return camera("color-profile", 0x42, profile), nil
}
func SetFocusMode(continuous bool) Command {
	code := byte(1)
	if continuous {
		code = 2
	}
	return camera("focus-mode", 0x24, code)
}
func SetPhotoFrame(square bool) Command {
	code := byte(1)
	if square {
		code = 3
	}
	return camera("photo-frame", 0x12, 0, code)
}
func SetPhotoRAW(enabled bool) Command {
	code := byte(1)
	if enabled {
		code = 2
	}
	return camera("photo-format", 0x16, code)
}
func SetCountdown(seconds int) (Command, error) {
	if seconds != 0 && seconds != 3 && seconds != 5 && seconds != 7 {
		return Command{}, errors.New("unmapped photo countdown")
	}
	return camera("photo-countdown", 0x4a, 0, 1, byte(seconds), 0, 0, 0), nil
}

// SetProperty is the native CameraAbstraction 02/8E TLV setter. A property
// identifier's presence does not establish firmware support for arbitrary data.
func SetProperty(id uint16, value []byte) (Command, error) {
	if len(value) > 255 {
		return Command{}, errors.New("property value exceeds one-byte length")
	}
	payload := []byte{1, 1, byte(id), byte(id >> 8), byte(len(value))}
	payload = append(payload, value...)
	return camera("set-property", 0x8e, payload...), nil
}

// GetProperty matches CameraAbstraction::SendOneTlvBufferGetPack in Mimo 2.12.1.
func GetProperty(id uint16) Command { return camera("get-property", 0x8e, 0, 1, byte(id), byte(id>>8)) }
func SetPro(enabled bool) Command {
	value := byte(0)
	if enabled {
		value = 1
	}
	cmd, _ := SetProperty(0, []byte{value})
	return cmd
}
func SetShowcase(enabled bool) Command {
	value := byte(0)
	if enabled {
		value = 1
	}
	cmd, _ := SetProperty(0x3b, []byte{1, value})
	return cmd
}
func SetAudioStereo(stereo bool) Command {
	value := byte(1)
	if stereo {
		value = 2
	}
	cmd, _ := SetProperty(0x20, []byte{value})
	return cmd
}

func SetTimelapse(intervalTenths uint16, durationSeconds uint32, output byte) (Command, error) {
	if output != 0 && output != 2 && output != 3 {
		return Command{}, errors.New("unmapped timelapse output")
	}
	p := make([]byte, 16)
	p[0] = 4
	p[2] = output
	binary.LittleEndian.PutUint16(p[3:], intervalTenths)
	binary.LittleEndian.PutUint32(p[5:], durationSeconds)
	return camera("timelapse", 0x6c, p...), nil
}
func SetHyperlapseSpeed(speed byte) (Command, error) {
	switch speed {
	case 0, 2, 5, 10, 15, 30:
		p := make([]byte, 16)
		p[0] = 11
		p[3] = speed
		return camera("hyperlapse-speed", 0x6c, p...), nil
	}
	return Command{}, errors.New("unmapped hyperlapse speed")
}
func SetPanorama(grid bool) Command {
	value := byte(5)
	if grid {
		value = 7
	}
	return camera("panorama-type", 0x6e, value)
}
func SetPanoramaRAW(raw bool) Command {
	value := byte(3)
	if raw {
		value = 1
	}
	return camera("panorama-format", 0xe7, value, 0)
}
func SetMotionlapseDirection(direction byte) (Command, error) {
	if direction > 2 {
		return Command{}, errors.New("unmapped motionlapse direction")
	}
	return SetProperty(0x37, []byte{direction})
}
func MotionlapsePreview() Command { cmd, _ := SetProperty(0x36, []byte{0, 0}); return cmd }

// MotionlapsePoint uses tenths of degrees. The confirmed on-wire axis order is
// yaw, roll, pitch; it differs from SDK Attitude's pitch, roll, yaw ABI order.
type MotionlapsePoint struct {
	Slot            int
	IntervalTenths  uint16
	DurationSeconds uint32
	PitchTenths     int16
	RollTenths      int16
	YawTenths       int16
}

func (p MotionlapsePoint) Command(remove bool) (Command, error) {
	if p.Slot < 1 || p.Slot > 4 {
		return Command{}, errors.New("motionlapse slot outside 1..4")
	}
	out := make([]byte, 16)
	out[0] = 5
	token := uint16(5 + (p.Slot-1)*4)
	if remove {
		token++
	}
	binary.LittleEndian.PutUint16(out[1:], token)
	binary.LittleEndian.PutUint16(out[3:], p.IntervalTenths)
	binary.LittleEndian.PutUint32(out[5:], p.DurationSeconds)
	if !remove {
		binary.LittleEndian.PutUint16(out[9:], uint16(p.YawTenths))
		binary.LittleEndian.PutUint16(out[11:], uint16(p.RollTenths))
		binary.LittleEndian.PutUint16(out[13:], uint16(p.PitchTenths))
	}
	return camera("motionlapse-point", 0x6c, out...), nil
}

func Joystick(pitchOffset, yawOffset int) (Command, error) {
	if pitchOffset < -550 || pitchOffset > 550 || yawOffset < -550 || yawOffset > 550 {
		return Command{}, errors.New("joystick offset outside captured +/-550 range")
	}
	p := []byte{0, 0, 0, 0, 0, 0, 0, 0x80, 0x22, 0}
	binary.LittleEndian.PutUint16(p, uint16(1024+pitchOffset))
	binary.LittleEndian.PutUint16(p[4:], uint16(1024+yawOffset))
	return Command{Name: "gimbal-joystick", Receiver: 4, Set: 4, ID: 1, Flags: 0, Payload: p}, nil
}
func Neutral() Command { cmd, _ := Joystick(0, 0); return cmd }
func Recenter() Command {
	return Command{Name: "gimbal-recenter", Receiver: 4, Set: 4, ID: 0x4c, Flags: 0x40, Payload: []byte{0xfe, 8}}
}
func GimbalProbe() Command {
	return Command{Name: "gimbal-probe", Receiver: 4, Set: 4, ID: 0x50, Flags: 0x40, Payload: []byte{1, 4, 5}}
}
func GimbalInitialize() Command {
	return Command{Name: "gimbal-initialize", Receiver: 3, Set: 3, ID: 0xda, Flags: 0x40, Payload: []byte{5, 0xff, 0xff, 0xff, 0xff}}
}

// ManualShutter accepts the explicitly mapped reciprocal denominators.
func ManualShutter(denominator float64) (Command, error) {
	if math.IsNaN(denominator) || math.IsInf(denominator, 0) {
		return Command{}, errors.New("invalid shutter denominator")
	}
	fractional := map[float64]uint16{1: 1, 1.25: 0x8001, 1.67: 0x8001, 2.5: 0x8002, 6.25: 0x8006, 12.5: 0x800c}
	sub := map[float64]byte{1.25: 25, 1.67: 67, 2.5: 5, 6.25: 25, 12.5: 5}
	allowed := map[int]bool{2: true, 3: true, 4: true, 5: true, 8: true, 10: true, 15: true, 20: true, 25: true, 30: true, 40: true, 50: true, 60: true, 80: true, 100: true, 120: true, 160: true, 200: true, 240: true, 320: true, 400: true, 500: true, 640: true, 800: true, 1000: true, 1250: true, 1600: true, 2000: true, 2500: true, 3200: true, 4000: true, 5000: true, 6400: true, 8000: true}
	encoded, ok := fractional[denominator]
	if !ok {
		if math.Trunc(denominator) != denominator || !allowed[int(denominator)] {
			return Command{}, errors.New("unmapped shutter denominator")
		}
		encoded = uint16(denominator) | 0x8000
	}
	p := []byte{1, byte(encoded), byte(encoded >> 8), sub[denominator], 0, 0, 0x40}
	return camera("manual-shutter", 0x28, p...), nil
}
