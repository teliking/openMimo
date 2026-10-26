package pocket3

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func assertPayload(t *testing.T, command Command, set, id byte, want string) {
	t.Helper()
	expected, err := hex.DecodeString(want)
	if err != nil {
		t.Fatal(err)
	}
	if command.Set != set || command.ID != id || !bytes.Equal(command.Payload, expected) {
		t.Fatalf("%02x/%02x %x != %02x/%02x %s", command.Set, command.ID, command.Payload, set, id, want)
	}
}
func TestPairIdentifierLimit(t *testing.T) {
	if _, err := Pair("12345678-1234-1234-1234-123456789abc", "osmo"); err == nil {
		t.Fatal("accepted 36-byte UUID that the camera rejects")
	}
	if _, err := Pair("12345678123412341234123456789abc", "osmo"); err != nil {
		t.Fatal(err)
	}
}
func TestPublishedGimbalVectors(t *testing.T) {
	data, err := os.ReadFile("../testdata/kaze-gimbal.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		ID        string `json:"id"`
		Operation string `json:"operation"`
		Input     struct {
			Pitch int `json:"pitchOffset"`
			Yaw   int `json:"yawOffset"`
		} `json:"input"`
		Expected struct {
			Payload string `json:"payloadHex"`
		} `json:"expected"`
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, v := range fixtures {
		t.Run(v.ID, func(t *testing.T) {
			switch v.Operation {
			case "gimbal.velocity":
				cmd, err := Joystick(v.Input.Pitch, v.Input.Yaw)
				if err != nil {
					t.Fatal(err)
				}
				assertPayload(t, cmd, 4, 1, v.Expected.Payload)
			case "gimbal.recenter":
				assertPayload(t, Recenter(), 4, 0x4c, v.Expected.Payload)
			default:
				t.Fatal("unhandled gimbal fixture")
			}
		})
	}
}
func TestAPKVideoFormatPreservesOpaqueBits(t *testing.T) {
	original := VideoFormat{16, 3, 0xa5, 0xc0, 0xbe}
	changed, err := original.WithFormat(10, 6).WithSpeedRatio(4)
	if err != nil {
		t.Fatal(err)
	}
	assertPayload(t, changed.Command(), 2, 0x18, "0a06a5c4be")
	if original != (VideoFormat{16, 3, 0xa5, 0xc0, 0xbe}) {
		t.Fatal("format mutated original state")
	}
}
func TestWireDiffersFromSDKABI(t *testing.T) {
	assertPayload(t, Record(true), 2, 2, "01")
	assertPayload(t, Record(false), 2, 2, "00")
	assertPayload(t, GetProperty(0x1234), 2, 0x8e, "00013412")
	cmd, err := SetISOMax(6400)
	if err != nil {
		t.Fatal(err)
	}
	assertPayload(t, cmd, 2, 0x8e, "01010f000107")
}
func TestMotionlapseAxisOrder(t *testing.T) {
	cmd, err := (MotionlapsePoint{Slot: 1, IntervalTenths: 5, DurationSeconds: 300, PitchTenths: 100, RollTenths: 200, YawTenths: 300}).Command(false)
	if err != nil {
		t.Fatal(err)
	}
	assertPayload(t, cmd, 2, 0x6c, "05050005002c0100002c01c800640000")
}
func TestRejectInvalidCameraValues(t *testing.T) {
	if _, err := SetManualISO(123); err == nil {
		t.Fatal("accepted unknown ISO")
	}
	if _, err := Joystick(551, 0); err == nil {
		t.Fatal("accepted excessive joystick offset")
	}
	if _, err := SubscribeProperty("cam\x00name", 1); err == nil {
		t.Fatal("accepted NUL property name")
	}
	if _, err := ParseBattery(make([]byte, 32)); err == nil {
		t.Fatal("accepted short battery telemetry")
	}
	if _, err := ParseGimbal(make([]byte, 5)); err == nil {
		t.Fatal("accepted short gimbal telemetry")
	}
}
func TestUnknownChargingIsPreserved(t *testing.T) {
	payload := make([]byte, 33)
	payload[20] = 80
	payload[32] = 5
	b, err := ParseBattery(payload)
	if err != nil {
		t.Fatal(err)
	}
	if b.Charging != nil || b.RawCharging != 5 {
		t.Fatal("invented charging state")
	}
}

func TestNativeGimbalEncoders(t *testing.T) {
	// Independent vectors derived from native little-endian stores and flags.
	assertPayload(t, (AnglePayload{YawTenths: -1200, RollTenths: 15, PitchTenths: -300, Absolute: true, IgnoreRoll: true, DurationTicks: 20}).Command(), 4, 0x14, "50fb0f00d4fe0514")
	assertPayload(t, (SpeedPayload{Yaw: -100, Roll: 20, Pitch: -30}).Command(), 4, 0x0c, "9cff1400e2ff8100")
}

func TestPublishedCameraVectors(t *testing.T) {
	data, err := os.ReadFile("../testdata/kaze-camera.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		ID        string `json:"id"`
		Operation string `json:"operation"`
		Input     struct {
			ISO json.RawMessage `json:"iso"`
		} `json:"input"`
		Expected struct {
			Set     string `json:"commandSetHex"`
			ID      string `json:"commandIdHex"`
			Payload string `json:"payloadHex"`
		} `json:"expected"`
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, v := range fixtures {
		t.Run(v.ID, func(t *testing.T) {
			var iso int
			if v.Operation == "camera.setManualIso" {
				var value string
				if err = json.Unmarshal(v.Input.ISO, &value); err != nil {
					t.Fatal(err)
				}
				if value != "AUTO" {
					iso, err = strconv.Atoi(value)
					if err != nil {
						t.Fatal(err)
					}
				}
			} else if err = json.Unmarshal(v.Input.ISO, &iso); err != nil {
				t.Fatal(err)
			}
			var command Command
			switch v.Operation {
			case "camera.setManualIso":
				command, err = SetManualISO(iso)
			case "camera.setIsoMax":
				command, err = SetISOMax(iso)
			default:
				t.Fatal("unhandled camera vector")
			}
			if err != nil {
				t.Fatal(err)
			}
			set, _ := strconv.ParseUint(v.Expected.Set, 16, 8)
			id, _ := strconv.ParseUint(v.Expected.ID, 16, 8)
			assertPayload(t, command, byte(set), byte(id), v.Expected.Payload)
		})
	}
}
