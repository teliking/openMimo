// Package datalink implements Pocket 3 UDP datagram framing and ACK windows.
// The layout follows the MIT-licensed Kaze for DJI capture-derived reference.
package datalink

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const HeaderSize = 8
const RoutingSize = 12
const MaxDatagramSize = 0x3fff
const (
	Handshake       byte = 0
	Status          byte = 1
	Media           byte = 2
	Data            byte = 3
	Acknowledgement byte = 4
	Command         byte = 5
)

type Datagram struct {
	Session  uint16
	Sequence uint16
	Type     byte
	Payload  []byte
}

func (d Datagram) MarshalBinary() ([]byte, error) {
	size := HeaderSize + len(d.Payload)
	if size > MaxDatagramSize {
		return nil, errors.New("datalink datagram exceeds 14-bit length")
	}
	out := make([]byte, size)
	binary.LittleEndian.PutUint16(out, uint16(size)|0x8000)
	binary.LittleEndian.PutUint16(out[2:], d.Session)
	binary.LittleEndian.PutUint16(out[4:], d.Sequence)
	out[6] = d.Type
	for _, b := range out[:7] {
		out[7] ^= b
	}
	copy(out[8:], d.Payload)
	return out, nil
}
func Parse(data []byte) (Datagram, error) {
	var d Datagram
	if len(data) < HeaderSize {
		return d, errors.New("truncated datalink header")
	}
	word := binary.LittleEndian.Uint16(data)
	if word&0x8000 == 0 {
		return d, errors.New("invalid datalink header marker")
	}
	if int(word&0x3fff) != len(data) {
		return d, fmt.Errorf("datalink length mismatch: %d vs %d", word&0x3fff, len(data))
	}
	var checksum byte
	for _, b := range data[:7] {
		checksum ^= b
	}
	if checksum != data[7] {
		return d, errors.New("datalink header XOR mismatch")
	}
	d = Datagram{Session: binary.LittleEndian.Uint16(data[2:]), Sequence: binary.LittleEndian.Uint16(data[4:]), Type: data[6], Payload: append([]byte(nil), data[8:]...)}
	return d, nil
}

type Routing struct {
	PeerAck  uint16
	Sequence uint16
	Counter  byte
	Class    byte
}

func (r Routing) MarshalBinary() []byte {
	out := make([]byte, RoutingSize)
	binary.LittleEndian.PutUint16(out, r.PeerAck)
	binary.LittleEndian.PutUint16(out[2:], r.Sequence)
	out[8] = r.Counter
	out[9] = 1
	out[10] = r.Class
	return out
}
func ParseRouting(data []byte) (Routing, error) {
	if len(data) < RoutingSize {
		return Routing{}, errors.New("truncated datalink routing header")
	}
	return Routing{PeerAck: binary.LittleEndian.Uint16(data), Sequence: binary.LittleEndian.Uint16(data[2:]), Counter: data[8], Class: data[10]}, nil
}

type AckWindow struct {
	RxType2   uint16
	RxType3   uint16
	PeerAckTx uint16
	LastTx    uint16
}

func (w AckWindow) MarshalBinary() []byte {
	out := make([]byte, 26)
	for _, pair := range []struct {
		offset int
		value  uint16
	}{{0, w.RxType2}, {2, w.RxType2}, {8, w.RxType3}, {10, w.RxType3}, {16, w.PeerAckTx}, {18, w.LastTx}} {
		binary.LittleEndian.PutUint16(out[pair.offset:], pair.value)
	}
	return out
}
func (w AckWindow) TxLagSlots() uint16 { return (w.LastTx - w.PeerAckTx) / 8 }

// Newer compares rolling 16-bit sequence numbers within a half-range window.
func Newer(sequence, previous uint16) bool { return int16(sequence-previous) > 0 }
