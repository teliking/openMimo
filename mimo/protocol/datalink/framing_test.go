package datalink

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func wantHex(t *testing.T, data []byte, want string) {
	t.Helper()
	b, err := hex.DecodeString(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, b) {
		t.Fatalf("%x != %x", data, b)
	}
}
func TestPublishedTransportFixtures(t *testing.T) {
	d := Datagram{Session: 0x1234, Sequence: 0x5678, Type: Command, Payload: make([]byte, 34)}
	b, err := d.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	wantHex(t, b[:8], "2A803412785605A7")
	parsed, err := Parse(b)
	if err != nil || parsed.Session != d.Session || parsed.Sequence != d.Sequence {
		t.Fatalf("%+v %v", parsed, err)
	}
	wantHex(t, (Routing{PeerAck: 0x1234, Sequence: 0x5678, Counter: 0x9a}).MarshalBinary(), "34127856000000009A010000")
	wantHex(t, (AckWindow{RxType2: 0x1111, RxType3: 0x2222, PeerAckTx: 0x3333, LastTx: 0x4444}).MarshalBinary(), "1111111100000000222222220000000033334444000000000000")
}
func TestRejectDamagedDatagrams(t *testing.T) {
	b, _ := (Datagram{Session: 1, Payload: []byte{1, 2}}).MarshalBinary()
	for i := 0; i < 8; i++ {
		bad := append([]byte(nil), b...)
		bad[i] ^= 1
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted damaged header byte %d", i)
		}
	}
	if _, err := Parse(b[:len(b)-1]); err == nil {
		t.Fatal("accepted truncated datagram")
	}
}
func TestSequenceWrap(t *testing.T) {
	if !Newer(0, 0xfff8) || Newer(0xfff8, 0) || Newer(8, 8) {
		t.Fatal("invalid wrap ordering")
	}
	if (AckWindow{LastTx: 8, PeerAckTx: 0xfff8}).TxLagSlots() != 2 {
		t.Fatal("invalid lag at wrap")
	}
}
