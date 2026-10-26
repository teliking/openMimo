// Package duml implements version-one DJI DUML framing.
// Framing is checked against capture-derived Kaze for DJI regression vectors.
package duml

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const HeaderSize = 11
const MinFrameSize = 13
const MaxFrameSize = 1023

var (
	ErrTruncated = errors.New("truncated DUML frame")
	ErrMagic     = errors.New("invalid DUML start byte")
	ErrLength    = errors.New("invalid DUML frame length")
	ErrVersion   = errors.New("unsupported DUML version")
	ErrHeaderCRC = errors.New("DUML header CRC8 mismatch")
	ErrCRC       = errors.New("DUML CRC16 mismatch")
)

type Frame struct {
	Sender     byte
	Receiver   byte
	Sequence   uint16
	Flags      byte
	CommandSet byte
	CommandID  byte
	Payload    []byte
}

func Address(component, index byte) (byte, error) {
	if component > 31 || index > 7 {
		return 0, errors.New("DUML address outside 5-bit type / 3-bit index")
	}
	return component | (index << 5), nil
}
func Component(address byte) byte { return address & 31 }
func Index(address byte) byte     { return address >> 5 }
func (f Frame) IsResponse() bool  { return f.Flags&0x80 != 0 }
func (f Frame) AckType() byte     { return (f.Flags >> 5) & 3 }
func (f Frame) Encryption() byte  { return f.Flags & 7 }

func CRC8(data []byte) byte {
	crc := byte(0x77)
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8c
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
func CRC16(data []byte) uint16 {
	crc := uint16(0x3692)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func (f Frame) MarshalBinary() ([]byte, error) {
	size := MinFrameSize + len(f.Payload)
	if size > MaxFrameSize {
		return nil, fmt.Errorf("%w: %d", ErrLength, size)
	}
	out := make([]byte, size)
	out[0] = 0x55
	binary.LittleEndian.PutUint16(out[1:3], uint16(size)|(1<<10))
	out[3] = CRC8(out[:3])
	out[4] = f.Sender
	out[5] = f.Receiver
	binary.LittleEndian.PutUint16(out[6:8], f.Sequence)
	out[8] = f.Flags
	out[9] = f.CommandSet
	out[10] = f.CommandID
	copy(out[11:], f.Payload)
	binary.LittleEndian.PutUint16(out[size-2:], CRC16(out[:size-2]))
	return out, nil
}

func Parse(data []byte) (Frame, error) {
	var f Frame
	if len(data) < MinFrameSize {
		return f, ErrTruncated
	}
	if data[0] != 0x55 {
		return f, ErrMagic
	}
	word := binary.LittleEndian.Uint16(data[1:3])
	size := int(word & 0x3ff)
	if size < MinFrameSize || size != len(data) {
		return f, fmt.Errorf("%w: declared=%d actual=%d", ErrLength, size, len(data))
	}
	if word>>10 != 1 {
		return f, ErrVersion
	}
	if CRC8(data[:3]) != data[3] {
		return f, ErrHeaderCRC
	}
	if CRC16(data[:size-2]) != binary.LittleEndian.Uint16(data[size-2:]) {
		return f, ErrCRC
	}
	f = Frame{Sender: data[4], Receiver: data[5], Sequence: binary.LittleEndian.Uint16(data[6:8]), Flags: data[8], CommandSet: data[9], CommandID: data[10], Payload: append([]byte(nil), data[11:size-2]...)}
	return f, nil
}

// Stream accepts fragmented/concatenated BLE or TCP data and resynchronizes after
// noise or invalid frames. A Stream belongs to a single receive stream.
type Stream struct {
	buffer    []byte
	Discarded uint64
}

func (s *Stream) Reset()        { s.buffer = nil; s.Discarded = 0 }
func (s *Stream) Buffered() int { return len(s.buffer) }
func (s *Stream) Feed(data []byte) []Frame {
	var frames []Frame
	for _, b := range data {
		s.buffer = append(s.buffer, b)
		for len(s.buffer) > 0 {
			if s.buffer[0] != 0x55 {
				s.buffer = s.buffer[1:]
				s.Discarded++
				continue
			}
			if len(s.buffer) < 4 {
				break
			}
			word := binary.LittleEndian.Uint16(s.buffer[1:3])
			size := int(word & 0x3ff)
			if size < MinFrameSize || word>>10 != 1 || CRC8(s.buffer[:3]) != s.buffer[3] {
				s.buffer = s.buffer[1:]
				s.Discarded++
				continue
			}
			if len(s.buffer) < size {
				break
			}
			frame, err := Parse(s.buffer[:size])
			if err != nil {
				s.buffer = s.buffer[1:]
				s.Discarded++
				continue
			}
			frames = append(frames, frame)
			s.buffer = s.buffer[size:]
		}
	}
	if len(s.buffer) == 0 {
		s.buffer = nil
	}
	return frames
}

// Scan finds CRC-valid frames in a complete datagram, including tunnel payloads.
// Unlike Stream, it deliberately checks every start position for nested frames.
func Scan(data []byte) []Frame {
	var found []Frame
	for i := 0; i+MinFrameSize <= len(data); i++ {
		if data[i] != 0x55 {
			continue
		}
		size := int(binary.LittleEndian.Uint16(data[i+1:]) & 0x3ff)
		if size < MinFrameSize || size > len(data)-i {
			continue
		}
		if frame, err := Parse(data[i : i+size]); err == nil {
			found = append(found, frame)
		}
	}
	return found
}
