package pocket3

import "encoding/binary"

// AnglePayload is the eight-byte 04/14 native GetRotateAnglePack layout used by
// HG212GimbalAbs. DurationTicks preserves the raw wire field. Relative-angle
// conversion and firmware attitude limits belong to the control layer.
type AnglePayload struct {
	YawTenths     int16
	RollTenths    int16
	PitchTenths   int16
	Absolute      bool
	IgnoreYaw     bool
	IgnoreRoll    bool
	IgnorePitch   bool
	DurationTicks byte
}

func (a AnglePayload) Command() Command {
	p := make([]byte, 8)
	binary.LittleEndian.PutUint16(p, uint16(a.YawTenths))
	binary.LittleEndian.PutUint16(p[2:], uint16(a.RollTenths))
	binary.LittleEndian.PutUint16(p[4:], uint16(a.PitchTenths))
	p[7] = a.DurationTicks
	if a.Absolute {
		p[6] |= 1
	}
	if a.IgnoreYaw {
		p[6] |= 2
	}
	if a.IgnoreRoll {
		p[6] |= 4
	}
	if a.IgnorePitch {
		p[6] |= 8
	}
	return Command{Name: "gimbal-angle-native", Receiver: 4, Set: 4, ID: 0x14, Flags: 0x40, Payload: p}
}

// SpeedPayload is HG212's native 04/0C encoder: yaw, roll, pitch as signed
// fixed-point values, followed by 81 00. Values are ten times the SDK values.
type SpeedPayload struct {
	Yaw   int16
	Roll  int16
	Pitch int16
}

func (s SpeedPayload) Command() Command {
	p := []byte{0, 0, 0, 0, 0, 0, 0x81, 0}
	binary.LittleEndian.PutUint16(p, uint16(s.Yaw))
	binary.LittleEndian.PutUint16(p[2:], uint16(s.Roll))
	binary.LittleEndian.PutUint16(p[4:], uint16(s.Pitch))
	return Command{Name: "gimbal-speed-native", Receiver: 4, Set: 4, ID: 0x0c, Flags: 0x40, Payload: p}
}
