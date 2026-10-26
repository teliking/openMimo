package pocket3

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestHG212VideoSetterDiffersFromCachedLegacyModule(t *testing.T) {
	cmd, err := (VideoSettings{Resolution: 16, FrameRate: 7, SpeedRatio: 4}).Command()
	if err != nil {
		t.Fatal(err)
	}
	assertPayload(t, cmd, 2, 0x18, "1007000400")
	legacy, err := (VideoFormat{16, 7, 0xa5, 0xc0, 0xbe}).WithSpeedRatio(4)
	if err != nil {
		t.Fatal(err)
	}
	assertPayload(t, legacy.Command(), 2, 0x18, "1007a5c4be")
	if _, err = (VideoSettings{SpeedRatio: 64}).Command(); err == nil {
		t.Fatal("accepted invalid speed ratio")
	}
}
func TestNativeResolutionAndFrameRateConversions(t *testing.T) {
	if SDKResolutionToWire(98) != 101 || WireResolutionToSDK(101) != 98 {
		t.Fatal("lost non-identity native resolution pair")
	}
	for _, code := range []uint32{0, 16, 45, 66, 106, 255} {
		if SDKResolutionToWire(code) != byte(code) {
			t.Fatalf("unexpected resolution mapping %d", code)
		}
	}
	if SDKFrameRateToWire(0xffff) != 0 || WireFrameRateToSDK(0) != 0xffff {
		t.Fatal("unknown FPS conversion differs from native")
	}
	if WireFrameRateToSDK(21) != 21 || WireFrameRateToSDK(22) != 22 {
		t.Fatal("ignored native lookup mask")
	}
	if WireFrameRateToSDK(23) != 23 || WireFrameRateToSDK(240) != 240 {
		t.Fatal("lost unknown numeric FPS")
	}
}
func TestVideoV2DoesNotAliasLegacyFormat(t *testing.T) {
	data, _ := hex.DecodeString("1006ffad01020304b1eeee")
	v, err := ParseVideoParametersV2(data)
	if err != nil {
		t.Fatal(err)
	}
	if v.Resolution != 16 || v.FrameRate != 6 || v.SpeedRatioFlag != 1 || v.Compression != 1 {
		t.Fatalf("bad native V2 fields: %+v", v)
	}
	data[0] = 0
	if v.Raw[0] != 16 || len(v.Raw) != 11 {
		t.Fatal("lost/copied incorrectly opaque V2 data")
	}
	if _, err = ParseVideoParametersV2(data[:8]); err == nil {
		t.Fatal("accepted truncated V2")
	}
}
func TestCapabilityFormatEntries(t *testing.T) {
	raw, _ := hex.DecodeString("01aabb021006f10a0302eeee")
	c, err := ParseVideoFormatCapabilities(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Formats) != 2 || c.Header != [2]byte{0xaa, 0xbb} || c.Formats[0].RawOptions != 0xf1 || !bytes.Equal(c.Tail, []byte{0xee, 0xee}) {
		t.Fatalf("capabilities %+v", c)
	}
	if err = c.CheckFormat(16, 6); err != nil {
		t.Fatal(err)
	}
	if err = c.CheckFormat(16, 7); err == nil {
		t.Fatal("claimed unavailable combination")
	}
	if _, err = ParseVideoFormatCapabilities(raw[:9]); err == nil {
		t.Fatal("accepted truncated capability entry")
	}
	unavailable, err := ParseVideoFormatCapabilities([]byte{0, 0, 0, 255, 1, 2})
	if err != nil || len(unavailable.Formats) != 0 || len(unavailable.Tail) != 2 {
		t.Fatal("native availability gate not respected")
	}
}
func TestPropertyUnknownStatesAndTruncation(t *testing.T) {
	for _, name := range CoreProperties() {
		if _, err := DecodeProperty(name, nil); err == nil {
			t.Fatalf("accepted truncated %s", name)
		}
	}
	if _, err := DecodeProperty("unmapped", []byte{0xff}); !errors.Is(err, ErrUnknownProperty) {
		t.Fatal(err)
	}
	v, err := DecodeProperty("cam_image_effect", []byte{0, 0, 0x3c, 0, 0xff, 50})
	if err != nil {
		t.Fatal(err)
	}
	if v.(ImageEffect).Kelvin != nil {
		t.Fatal("invented Kelvin for unknown white-balance mode")
	}
	v, err = DecodeProperty("cam_lens_state", []byte{0xff})
	if err != nil || v.(LensState).FocusMode != nil {
		t.Fatal("invented unknown focus state")
	}
	v, err = DecodeProperty("cam_motionlapse_params", []byte{0, 0, 2, 0xff, 0, 0, 0, 3})
	if err != nil || v.(MotionlapseParameters).WaypointCount != nil {
		t.Fatal("invented custom waypoint count")
	}
}

func TestCapturedPropertyVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/kaze-property.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Subscription string `json:"subscription"`
		Vectors      []struct {
			Name    string         `json:"name"`
			Payload string         `json:"payload"`
			Fields  map[string]any `json:"fields"`
		} `json:"vectors"`
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	command, err := SubscribeProperty("cam_video_param_v2", 0xc938)
	if err != nil {
		t.Fatal(err)
	}
	assertPayload(t, command, 0, 0x99, fixtures.Subscription)
	for _, v := range fixtures.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			payload, err := hex.DecodeString(v.Payload)
			if err != nil {
				t.Fatal(err)
			}
			p, err := ParsePropertyPush(payload)
			if err != nil {
				t.Fatal(err)
			}
			if p.Name != v.Name {
				t.Fatalf("wrong property %s", p.Name)
			}
			decoded, err := DecodeProperty(p.Name, p.Value)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err = json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for name, want := range v.Fields {
				if fields[name] != want {
					t.Fatalf("%s: got %v, want %v", name, fields[name], want)
				}
			}
		})
	}
}
