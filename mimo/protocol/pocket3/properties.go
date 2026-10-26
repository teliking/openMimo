package pocket3

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrUnknownProperty = errors.New("unmapped camera property")

// CoreProperties returns a private list of camera state topics used by monitor.
func CoreProperties() []string {
	return []string{"cam_video_param_v2", "cam_sensor_aspect_ratio", "cam_image_effect", "cam_expo_param", "cam_lens_state", "cam_photo_param", "cam_lapse_param", "cam_pano_params", "cam_motionlapse_params", "camcap_video_format"}
}

// These state types expose numeric wire values. Unknown selectors stay numeric;
// their presence does not establish a writable setting on the current firmware.
type SensorAspectRatio struct {
	Code byte
	Raw  []byte
}
type ImageEffect struct {
	ColorProfile     byte
	WhiteBalanceMode byte
	Kelvin           *uint16
	Raw              []byte
}
type ExposureParameters struct {
	EVCode       byte
	EVThirds     *int
	Mode         byte
	EffectiveISO uint32
	Raw          []byte
}
type LensState struct {
	RawFocus  byte
	FocusMode *byte
	Raw       []byte
}
type PhotoParameters struct {
	Frame            byte
	Format           byte
	CountdownSeconds byte
	Raw              []byte
}
type LapseParameters struct {
	Output          byte
	IntervalTenths  uint16
	DurationSeconds uint32
	SpeedA          uint16
	SpeedB          uint16
	Raw             []byte
}
type PanoramaParameters struct {
	Type   byte
	Format byte
	Raw    []byte
}
type MotionlapseParameters struct {
	Direction     byte
	RawPreview    byte
	WaypointCount *byte
	Raw           []byte
}

// DecodeProperty maps fields independently confirmed in native callbacks or the
// attributed capture-derived reference. Composite fields remain as Raw bytes.
func DecodeProperty(name string, data []byte) (any, error) {
	minimum := map[string]int{"cam_video_param_v2": 9, "cam_sensor_aspect_ratio": 1, "cam_image_effect": 6, "cam_expo_param": 20, "cam_lens_state": 1, "cam_photo_param": 13, "cam_lapse_param": 21, "cam_pano_params": 3, "cam_motionlapse_params": 8, "camcap_video_format": 4}
	n, known := minimum[name]
	if !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProperty, name)
	}
	if len(data) < n {
		return nil, fmt.Errorf("truncated %s: need %d bytes, got %d", name, n, len(data))
	}
	raw := append([]byte(nil), data...)
	switch name {
	case "cam_video_param_v2":
		return ParseVideoParametersV2(data)
	case "camcap_video_format":
		return ParseVideoFormatCapabilities(data)
	case "cam_sensor_aspect_ratio":
		return SensorAspectRatio{Code: data[0], Raw: raw}, nil
	case "cam_image_effect":
		v := ImageEffect{ColorProfile: data[2], WhiteBalanceMode: data[4], Raw: raw}
		if data[4] == 6 {
			kelvin := uint16(data[5]) * 100
			v.Kelvin = &kelvin
		}
		return v, nil
	case "cam_expo_param":
		v := ExposureParameters{EVCode: data[6], Mode: data[7], EffectiveISO: binary.LittleEndian.Uint32(data[16:]), Raw: raw}
		if data[6] >= 7 && data[6] <= 25 {
			thirds := int(data[6]) - 16
			v.EVThirds = &thirds
		}
		return v, nil
	case "cam_lens_state":
		v := LensState{RawFocus: data[0], Raw: raw}
		if data[0] == 0xb1 || data[0] == 0xb2 {
			mode := data[0] & 15
			v.FocusMode = &mode
		}
		return v, nil
	case "cam_photo_param":
		return PhotoParameters{Frame: data[1], Format: data[3], CountdownSeconds: data[7], Raw: raw}, nil
	case "cam_lapse_param":
		return LapseParameters{Output: data[0], IntervalTenths: binary.LittleEndian.Uint16(data[1:]), DurationSeconds: binary.LittleEndian.Uint32(data[5:]), SpeedA: binary.LittleEndian.Uint16(data[9:]), SpeedB: binary.LittleEndian.Uint16(data[11:]), Raw: raw}, nil
	case "cam_pano_params":
		return PanoramaParameters{Type: data[0], Format: data[1], Raw: raw}, nil
	case "cam_motionlapse_params":
		v := MotionlapseParameters{Direction: data[2], RawPreview: data[3], Raw: raw}
		if data[2] == 0 && data[7] <= 4 {
			count := data[7]
			v.WaypointCount = &count
		}
		return v, nil
	}
	return nil, ErrUnknownProperty
}
