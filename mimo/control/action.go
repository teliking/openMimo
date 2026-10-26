// Package control validates app/script actions before creating camera commands.
package control

import (
	"errors"
	"fmt"
	"openmimo/protocol/pocket3"
)

type Action struct {
	Type               string  `json:"type"`
	Mode               byte    `json:"mode,omitempty"`
	Resolution         byte    `json:"resolution,omitempty"`
	FPS                byte    `json:"fps,omitempty"`
	SpeedRatio         byte    `json:"speed_ratio,omitempty"`
	ISO                int     `json:"iso,omitempty"`
	EVThirds           int     `json:"ev_thirds,omitempty"`
	Kelvin             int     `json:"kelvin,omitempty"`
	ShutterDenominator float64 `json:"shutter_denominator,omitempty"`
	Enabled            bool    `json:"enabled,omitempty"`
	Remove             bool    `json:"remove,omitempty"`
	Profile            byte    `json:"profile,omitempty"`
	Pitch              int     `json:"pitch,omitempty"`
	Yaw                int     `json:"yaw,omitempty"`
	Roll               int     `json:"roll,omitempty"`
	DurationMS         int     `json:"duration_ms,omitempty"`
	DurationTicks      byte    `json:"duration_ticks,omitempty"`
	IntervalTenths     uint16  `json:"interval_tenths,omitempty"`
	Seconds            uint32  `json:"seconds,omitempty"`
	Output             byte    `json:"output,omitempty"`
	Slot               int     `json:"slot,omitempty"`
}

func (a Action) Command() (pocket3.Command, error) {
	switch a.Type {
	case "record-start":
		return pocket3.Record(true), nil
	case "record-stop":
		return pocket3.Record(false), nil
	case "photo":
		return pocket3.ShootPhoto(), nil
	case "panorama-shoot":
		return pocket3.ShootPanorama(), nil
	case "mode":
		return pocket3.SetMode(pocket3.Mode(a.Mode))
	case "video-format":
		if a.FPS == 0 {
			return pocket3.Command{}, errors.New("frame-rate code must be specified")
		}
		return (pocket3.VideoSettings{Resolution: a.Resolution, FrameRate: a.FPS, SpeedRatio: a.SpeedRatio}).Command()
	case "compression":
		return pocket3.SetCompression(a.Enabled), nil
	case "exposure-manual":
		return pocket3.SetExposureMode(a.Enabled), nil
	case "iso":
		return pocket3.SetManualISO(a.ISO)
	case "iso-max":
		return pocket3.SetISOMax(a.ISO)
	case "shutter":
		return pocket3.ManualShutter(a.ShutterDenominator)
	case "ev":
		return pocket3.SetEV(a.EVThirds)
	case "white-balance":
		return pocket3.WhiteBalanceKelvin(a.Kelvin)
	case "white-balance-auto":
		return pocket3.WhiteBalanceAuto(), nil
	case "color":
		return pocket3.SetColorProfile(a.Profile)
	case "focus-continuous":
		return pocket3.SetFocusMode(a.Enabled), nil
	case "pro":
		return pocket3.SetPro(a.Enabled), nil
	case "photo-square":
		return pocket3.SetPhotoFrame(a.Enabled), nil
	case "photo-raw":
		return pocket3.SetPhotoRAW(a.Enabled), nil
	case "countdown":
		return pocket3.SetCountdown(int(a.Seconds))
	case "showcase":
		return pocket3.SetShowcase(a.Enabled), nil
	case "audio-stereo":
		return pocket3.SetAudioStereo(a.Enabled), nil
	case "timelapse":
		if a.IntervalTenths == 0 {
			return pocket3.Command{}, errors.New("timelapse interval must be positive")
		}
		return pocket3.SetTimelapse(a.IntervalTenths, a.Seconds, a.Output)
	case "hyperlapse":
		return pocket3.SetHyperlapseSpeed(a.SpeedRatio)
	case "panorama-grid":
		return pocket3.SetPanorama(a.Enabled), nil
	case "panorama-raw":
		return pocket3.SetPanoramaRAW(a.Enabled), nil
	case "motion-point":
		if a.Yaw < -32768 || a.Yaw > 32767 || a.Pitch < -32768 || a.Pitch > 32767 || a.Roll < -32768 || a.Roll > 32767 {
			return pocket3.Command{}, errors.New("waypoint attitude exceeds int16")
		}
		return (pocket3.MotionlapsePoint{Slot: a.Slot, IntervalTenths: a.IntervalTenths, DurationSeconds: a.Seconds, YawTenths: int16(a.Yaw), PitchTenths: int16(a.Pitch), RollTenths: int16(a.Roll)}).Command(a.Remove)
	case "motion-preview":
		return pocket3.MotionlapsePreview(), nil
	case "gimbal-stop":
		return pocket3.Neutral(), nil
	case "gimbal-recenter":
		return pocket3.Recenter(), nil
	case "gimbal-angle":
		if a.Roll != 0 {
			return pocket3.Command{}, errors.New("app absolute-angle control currently exposes yaw and pitch only")
		}
		if a.Yaw < -1800 || a.Yaw > 1800 || a.Pitch < -900 || a.Pitch > 900 || a.Roll < -450 || a.Roll > 450 {
			return pocket3.Command{}, errors.New("angle exceeds app trial bounds")
		}
		return (pocket3.AnglePayload{YawTenths: int16(a.Yaw), PitchTenths: int16(a.Pitch), RollTenths: int16(a.Roll), Absolute: true, IgnoreRoll: true, DurationTicks: a.DurationTicks}).Command(), nil
	case "gimbal-jog":
		if a.DurationMS < 50 || a.DurationMS > 60000 {
			return pocket3.Command{}, errors.New("jog duration requires 50..60000 ms")
		}
		return pocket3.Joystick(a.Pitch, a.Yaw)
	default:
		return pocket3.Command{}, fmt.Errorf("unknown action %q", a.Type)
	}
}
