package dmr

import (
	"math/rand"
	"testing"
)

func TestGolay2087Roundtrip(t *testing.T) {
	for v := 0; v < 256; v++ {
		enc := Golay2087Encode(byte(v))
		if got := Golay2087Decode(enc[:]); int(got) != v {
			t.Fatalf("value %d decoded as %d", v, got)
		}
	}
}

func TestQR1676Roundtrip(t *testing.T) {
	for b := 0; b < 256; b++ {
		enc := QR1676Encode(byte(b))
		if got := QR1676Decode(enc[:]); (got >> 1) != (byte(b) >> 1) {
			t.Fatalf("value %02x decoded as %02x", b, got)
		}
	}
}

func TestQR1676SingleBitCorrection(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		b := byte(rng.Intn(256))
		enc := QR1676Encode(b)
		bit := rng.Intn(16)
		enc[bit/8] ^= 1 << (bit % 8)
		if got := QR1676Decode(enc[:]); (got >> 1) != (b >> 1) {
			t.Fatalf("bit %d of %02x not corrected: got %02x", bit, b, got)
		}
	}
}

func TestRSEncodeCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 50; i++ {
		msg := make([]byte, 9)
		rng.Read(msg)
		par := RSEncode(msg, 9, 4)
		block := append(append([]byte(nil), msg...), par[2], par[1], par[0])
		if !RSCheck(block) {
			t.Fatalf("valid block rejected: %x", block)
		}
		bad := append([]byte(nil), block...)
		bad[0] ^= 1
		if RSCheck(bad) {
			t.Fatalf("corrupted block accepted")
		}
	}
}

func buildHeader(lc DMRLC, dataType byte, cc int) []byte {
	burst, _ := FullLCEncode(lc, dataType)
	NewSlotType(byte(cc), dataType).Encode(burst)
	AddDataSync(burst, false)
	return burst
}

func TestFullLCHeaderRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 40; i++ {
		src := rng.Intn(0xFFFFFF) + 1
		dst := rng.Intn(0xFFFFFF) + 1
		flco := byte(FLCOGroup)
		if i%2 == 1 {
			flco = FLCOUserUser
		}
		lc := NewDMRLC(flco, src, dst, 0x20)
		got, err := FullLCDecode(buildHeader(lc, DTVoiceLCHeader, 1), DTVoiceLCHeader)
		if err != nil {
			t.Fatal(err)
		}
		if got.SrcID != src || got.DstID != dst || got.FLCO != flco {
			t.Fatalf("got %+v, want src=%d dst=%d flco=%d", got, src, dst, flco)
		}
	}
}

func TestFullLCTerminatorRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 20; i++ {
		lc := NewDMRLC(FLCOGroup, rng.Intn(0xFFFFFF)+1, 9, 0)
		got, err := FullLCDecode(buildHeader(lc, DTTerminatorWithLC, 1), DTTerminatorWithLC)
		if err != nil {
			t.Fatal(err)
		}
		if got.SrcID != lc.SrcID {
			t.Fatalf("got src %d, want %d", got.SrcID, lc.SrcID)
		}
	}
}

func TestSlotTypeRoundtrip(t *testing.T) {
	b := make([]byte, 33)
	NewSlotType(3, 0x04).Encode(b)
	st := DecodeSlotType(b)
	if st.ColorCode != 3 || st.DataType != 0x04 {
		t.Fatalf("got %+v", st)
	}
}

func TestSyncInsert(t *testing.T) {
	b := make([]byte, 33)
	AddDataSync(b, false)
	if got := GetSyncType(b); got != "MS_DATA" {
		t.Fatalf("got %q", got)
	}
	b2 := make([]byte, 33)
	AddVoiceSync(b2, true)
	if got := GetSyncType(b2); got != "BS_VOICE" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbeddedLCAccumulate(t *testing.T) {
	lc := NewDMRLC(FLCOGroup, 2501234, 777, 0)
	bursts := [][]byte{make([]byte, 33), make([]byte, 33), make([]byte, 33), make([]byte, 33)}
	lcss := EmbedLCBlocks(lc, bursts)
	acc := NewEmbeddedLC()
	done := false
	for i, burst := range bursts {
		if acc.Add(burst, lcss[i]) {
			done = true
		}
	}
	if !done || acc.LC == nil {
		t.Fatal("embedded LC did not accumulate")
	}
	if acc.LC.SrcID != 2501234 || acc.LC.DstID != 777 {
		t.Fatalf("got %+v", acc.LC)
	}
}

func TestEmbeddedLCRequiresFullSequence(t *testing.T) {
	lc := NewDMRLC(FLCOGroup, 111, 222, 0)
	bursts := [][]byte{make([]byte, 33), make([]byte, 33), make([]byte, 33), make([]byte, 33)}
	lcss := EmbedLCBlocks(lc, bursts)
	acc := NewEmbeddedLC()
	for i := 0; i < 3; i++ {
		acc.Add(bursts[i], lcss[i])
	}
	if acc.Valid {
		t.Fatal("partial sequence must not be valid")
	}
}
