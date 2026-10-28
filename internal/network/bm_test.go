package network

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"dmr-hotspot/internal/protocol"
)

func TestPackUnpackDMRD(t *testing.T) {
	d := DMRData{SeqNo: 7, SlotNo: 2, SrcID: 2500001, DstID: 9, FLCO: 0,
		DataType: protocol.DTVoice, StreamID: 12345, N: 3,
		Data: bytes.Repeat([]byte{0xAB}, 33), BER: 2, RSSI: 70}
	raw := PackDMRD(d, 2500001)
	if len(raw) != protocol.HomebrewDataPacketLength {
		t.Fatalf("length %d", len(raw))
	}
	if got := binary.BigEndian.Uint32(raw[11:15]); got != 2500001 {
		t.Fatalf("my id %d", got)
	}
	got := UnpackDMRD(raw)
	if got == nil {
		t.Fatal("nil")
	}
	if got.SeqNo != d.SeqNo || got.SlotNo != d.SlotNo || got.SrcID != d.SrcID ||
		got.DstID != d.DstID || got.FLCO != d.FLCO || got.DataType != d.DataType ||
		got.StreamID != d.StreamID || got.N != d.N || got.BER != d.BER || got.RSSI != d.RSSI {
		t.Fatalf("got %+v want %+v", got, d)
	}
	if !bytes.Equal(got.Data, d.Data) {
		t.Fatal("payload mismatch")
	}
}

func TestUnpackDMRDRejectsInvalid(t *testing.T) {
	if UnpackDMRD([]byte("short")) != nil {
		t.Fatal("short packet accepted")
	}
	raw := PackDMRD(DMRData{SrcID: 1, SlotNo: 2, Data: make([]byte, 33)}, 1)
	raw[5], raw[6], raw[7] = 0, 0, 0
	if UnpackDMRD(raw) != nil {
		t.Fatal("zero source accepted")
	}
}

func TestBuildConfigBytes(t *testing.T) {
	c := BuildConfigBytes("N0CALL", 438800000, 439800000, 2, true, false, true, "test", 20)
	if len(c) != 294 {
		t.Fatalf("length %d", len(c))
	}
	if got := string(c[0:8]); got != "N0CALL  " {
		t.Fatalf("callsign %q", got)
	}
	if got := string(c[8:17]); got != "438800000" {
		t.Fatalf("rx %q", got)
	}
	if got := string(c[17:26]); got != "439800000" {
		t.Fatalf("tx %q", got)
	}
	if got := string(c[26:30]); got != "2002" {
		t.Fatalf("power/cc %q", got)
	}
	if c[89] != '1' {
		t.Fatalf("duplex slot flag %q", c[89])
	}
	if got := string(c[70:89]); got != "test               " {
		t.Fatalf("description %q", got)
	}
	if got := string(c[254:294]); got != "MMDVM_OpenGD77_HS"+strings.Repeat(" ", 23) {
		t.Fatalf("software %q", got)
	}
}

func TestBuildConfigSimplexSlotFlag(t *testing.T) {
	c := BuildConfigBytes("N0CALL", 438800000, 438800000, 1, false, true, false, "test", 20)
	if c[89] != '4' {
		t.Fatalf("simplex slot flag %q", c[89])
	}
}
