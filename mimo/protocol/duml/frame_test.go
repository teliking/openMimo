package duml

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestCapturedWakeFrame(t *testing.T) {
	want := unhex(t, "550F04A202F02B8040002B04002440")
	f := Frame{Sender: 2, Receiver: 0xf0, Sequence: 0x802b, Flags: 0x40, CommandSet: 0, CommandID: 0x2b, Payload: []byte{4, 0}}
	got, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%x != %x", got, want)
	}
	parsed, err := Parse(want)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Sequence != f.Sequence || parsed.Receiver != f.Receiver || !bytes.Equal(parsed.Payload, f.Payload) {
		t.Fatalf("%+v", parsed)
	}
}
func TestPublishedCRCFixtures(t *testing.T) {
	data, err := os.ReadFile("../testdata/kaze-crc.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		ID        string `json:"id"`
		Operation string `json:"operation"`
		Input     struct {
			DataHex  string `json:"dataHex"`
			FrameHex string `json:"frameHex"`
		} `json:"input"`
		Expected struct {
			CRC   string `json:"crcHex"`
			Valid bool   `json:"valid"`
		} `json:"expected"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.ID, func(t *testing.T) {
			data := unhex(t, v.Input.DataHex)
			expected := unhex(t, v.Expected.CRC)
			switch v.Operation {
			case "crc8.compute":
				if CRC8(data) != expected[0] {
					t.Fatal("CRC8 differs from reference")
				}
			case "crc16.compute":
				if CRC16(data) != uint16(expected[0])<<8|uint16(expected[1]) {
					t.Fatal("CRC16 differs from reference")
				}
			case "duml.rejectCorruptedFrame":
				if _, err := Parse(unhex(t, v.Input.FrameHex)); !errors.Is(err, ErrCRC) {
					t.Fatalf("expected CRC rejection, got %v", err)
				}
			default:
				t.Fatalf("unhandled fixture %s", v.Operation)
			}
		})
	}
}
func TestStreamRecoversAndReassembles(t *testing.T) {
	frame := unhex(t, "550F04A202F02B8040002B04002440")
	bad := append([]byte(nil), frame...)
	bad[11] ^= 1
	input := append([]byte{1, 2, 0x55, 0, 0, 0}, bad...)
	input = append(input, frame...)
	input = append(input, frame...)
	for split := 0; split <= len(input); split++ {
		var s Stream
		frames := s.Feed(input[:split])
		frames = append(frames, s.Feed(input[split:])...)
		if len(frames) != 2 || s.Buffered() != 0 {
			t.Fatalf("split=%d frames=%d buffered=%d", split, len(frames), s.Buffered())
		}
	}
}
func TestTenBitLength(t *testing.T) {
	f := Frame{Payload: make([]byte, MaxFrameSize-MinFrameSize)}
	b, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Parse(b); err != nil {
		t.Fatal(err)
	}
	f.Payload = append(f.Payload, 0)
	if _, err = f.MarshalBinary(); !errors.Is(err, ErrLength) {
		t.Fatal("oversize frame accepted")
	}
}
func FuzzParse(f *testing.F) {
	f.Add([]byte{0x55, 0x0f, 4, 0xa2})
	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := Parse(data)
		if err == nil {
			got, err := frame.MarshalBinary()
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("valid frame changed during re-encoding")
			}
		}
	})
}
