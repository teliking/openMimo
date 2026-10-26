package control

import "testing"

func TestRejectTruncationAndUnboundedMotion(t *testing.T) {
	for _, a := range []Action{{Type: "gimbal-angle", Yaw: 50000}, {Type: "gimbal-angle", Roll: 1}, {Type: "gimbal-jog", DurationMS: 60001}, {Type: "gimbal-jog", DurationMS: 100, Pitch: 1000}, {Type: "video-format", Resolution: 16}, {Type: "timelapse"}, {Type: "shutter", ShutterDenominator: 123}, {Type: "motion-point", Slot: 1, Yaw: 65536}} {
		if _, err := a.Command(); err == nil {
			t.Fatalf("accepted invalid action %+v", a)
		}
	}
}
