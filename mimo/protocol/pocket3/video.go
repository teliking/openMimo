package pocket3

import (
	"errors"
	"fmt"
)

// SDKResolutionToWire reproduces the native mapping and the caller's narrowing
// to one byte. It is a representation conversion, not a capability check.
func SDKResolutionToWire(code uint32) byte {
	for _, pair := range nativeResolutionPairs {
		if pair.sdk == code {
			return pair.wire
		}
	}
	return byte(code)
}
func WireResolutionToSDK(code byte) uint32 {
	for _, pair := range nativeResolutionPairs {
		if pair.wire == code {
			return pair.sdk
		}
	}
	return uint32(code)
}
func SDKFrameRateToWire(code uint32) byte {
	if code == 0xffff {
		return 0
	}
	return byte(code)
}
func WireFrameRateToSDK(code byte) uint32 {
	if code <= 23 && (uint32(0x9fffff)>>code)&1 != 0 {
		return nativeFrameRateFromWire[code]
	}
	return uint32(code)
}

// VideoSettings uses wire codes. Command follows HG212CameraAbs's registered
// VideoResolutionFrameRateAndSpeedRatioSet: [res fps 00 ratio&3f 00]. The zero
// fields are explicit native initialization, unlike the older module's cache.
type VideoSettings struct {
	Resolution byte
	FrameRate  byte
	SpeedRatio byte
}

func (v VideoSettings) Command() (Command, error) {
	if v.SpeedRatio > 63 {
		return Command{}, errors.New("video speed ratio exceeds six bits")
	}
	return camera("video-format-hg212", 0x18, v.Resolution, v.FrameRate, 0, v.SpeedRatio, 0), nil
}

// VideoParametersV2 preserves unknown fields instead of treating the topic as
// the old five-byte format. SpeedRatioFlag follows the native V2 callback's
// one-bit extraction; it does not establish the camera's numeric speed ratio.
type VideoParametersV2 struct {
	Resolution     byte
	FrameRate      byte
	SpeedRatioFlag byte
	Compression    byte
	Raw            []byte
}

func ParseVideoParametersV2(data []byte) (VideoParametersV2, error) {
	if len(data) < 9 {
		return VideoParametersV2{}, errors.New("truncated cam_video_param_v2")
	}
	return VideoParametersV2{Resolution: data[0], FrameRate: data[1], SpeedRatioFlag: data[3] & 1, Compression: data[8] & 15, Raw: append([]byte(nil), data...)}, nil
}

type VideoFormatCapability struct {
	Resolution byte
	FrameRate  byte
	RawOptions byte
}
type VideoFormatCapabilities struct {
	RawAvailability byte
	Header          [2]byte
	DeclaredCount   byte
	Formats         []VideoFormatCapability
	Tail            []byte
}

// ParseVideoFormatCapabilities follows KeyVideoResolutionFrameRateRangeTopicPush:
// byte 0 gates availability, byte 3 counts three-byte entries at offset 4.
// The third byte per entry and header bytes 1/2 remain uninterpreted.
func ParseVideoFormatCapabilities(data []byte) (VideoFormatCapabilities, error) {
	var c VideoFormatCapabilities
	if len(data) < 4 {
		return c, errors.New("truncated video format capability header")
	}
	c.RawAvailability, c.Header, c.DeclaredCount = data[0], [2]byte{data[1], data[2]}, data[3]
	end := 4
	if c.RawAvailability != 0 {
		end += int(c.DeclaredCount) * 3
		if end > len(data) {
			return VideoFormatCapabilities{}, errors.New("truncated video format capability entries")
		}
		for offset := 4; offset < end; offset += 3 {
			c.Formats = append(c.Formats, VideoFormatCapability{data[offset], data[offset+1], data[offset+2]})
		}
	}
	c.Tail = append([]byte(nil), data[end:]...)
	return c, nil
}

// CheckFormat only checks resolution and frame-rate membership. The mode and
// opaque options may impose additional restrictions on the camera.
func (c VideoFormatCapabilities) CheckFormat(resolution, frameRate byte) error {
	if c.RawAvailability == 0 {
		return errors.New("camera format capabilities unavailable")
	}
	for _, f := range c.Formats {
		if f.Resolution == resolution && f.FrameRate == frameRate {
			return nil
		}
	}
	return fmt.Errorf("format %02x/%02x absent from camera capability response", resolution, frameRate)
}
