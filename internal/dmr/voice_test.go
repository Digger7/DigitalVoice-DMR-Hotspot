package dmr

import (
	"bytes"
	"math/rand"
	"testing"
)

func randomBurst(rng *rand.Rand) []byte {
	b := make([]byte, 33)
	rng.Read(b)
	b[13] &= 0x0F
	b[19] &= 0xF0
	for i := 14; i < 19; i++ {
		b[i] = 0
	}
	return b
}

func TestVoiceRoundtripIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		b := randomBurst(rng)
		got := VoiceRoundtrip(b)
		if !bytes.Equal(got, b) {
			t.Fatalf("roundtrip mismatch\n got %x\nwant %x", got, b)
		}
	}
}

func TestVoiceFrameSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	for i := 0; i < 20; i++ {
		frames, err := ExtractVoiceFrames(randomBurst(rng))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range frames {
			if len(f) != 9 {
				t.Fatal("frame must be 9 bytes")
			}
		}
	}
}

func TestVoiceBadSizes(t *testing.T) {
	if _, err := BuildVoiceBurst([]byte("12345678"), make([]byte, 9), make([]byte, 9)); err == nil {
		t.Fatal("expected error for 8-byte frame")
	}
	if _, err := ExtractVoiceFrames([]byte("123")); err == nil {
		t.Fatal("expected error for short burst")
	}
}

func TestVoiceBitOrder(t *testing.T) {
	b := make([]byte, 33)
	b[0] = 0x80
	frames, err := ExtractVoiceFrames(b)
	if err != nil {
		t.Fatal(err)
	}
	if frames[0][0]&0x80 == 0 {
		t.Fatal("MSB of burst bit 0 must map to frame 1 MSB")
	}
}

func TestBuildExtractFrames(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for i := 0; i < 25; i++ {
		var frames [3][9]byte
		for f := 0; f < 3; f++ {
			rng.Read(frames[f][:])
		}
		burst, err := BuildVoiceBurst(frames[0][:], frames[1][:], frames[2][:])
		if err != nil {
			t.Fatal(err)
		}
		got, err := ExtractVoiceFrames(burst)
		if err != nil {
			t.Fatal(err)
		}
		if got != frames {
			t.Fatal("frames changed through burst")
		}
	}
}
