package core

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestPolishSpeechNormalisesAndTrims(t *testing.T) {
	const lead, tone, tail = 8000, 8000, 8000
	pcm := make([]byte, (lead+tone+tail)*2)
	for i := 0; i < tone; i++ {
		v := int16(3000 * math.Sin(2*math.Pi*440*float64(i)/8000))
		binary.LittleEndian.PutUint16(pcm[(lead+i)*2:], uint16(v))
	}
	out := PolishSpeech(pcm, 1.0)
	if len(out) == 0 {
		t.Fatal("PolishSpeech returned nothing")
	}
	if len(out) >= len(pcm) {
		t.Fatalf("silence was not trimmed: %d >= %d", len(out), len(pcm))
	}
	peak := 0
	for i := 0; i < len(out); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(out[i:])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak < 27500 || peak > 30000 {
		t.Fatalf("peak after polish = %d, want ~28835", peak)
	}
}

func biquadGain(f *biquad, hz float64) float64 {
	const rate = 8000
	var sum float64
	n := 0
	for i := 0; i < rate; i++ {
		y := f.process(math.Sin(2 * math.Pi * hz * float64(i) / rate))
		if i > rate/2 {
			sum += y * y
			n++
		}
	}
	return math.Sqrt(sum / float64(n))
}

func TestPolishSpeechBandLimits(t *testing.T) {
	reference := biquadGain(newHighPass(300, 8000, 0.707), 1000)
	if got := biquadGain(newHighPass(300, 8000, 0.707), 100); got > reference*0.2 {
		t.Fatalf("high-pass leaks 100 Hz: gain %.4f vs %.4f", got, reference)
	}
	reference = biquadGain(newLowPass(3400, 8000, 0.707), 1000)
	if got := biquadGain(newLowPass(3400, 8000, 0.707), 3700); got > reference*0.5 {
		t.Fatalf("low-pass leaks 3700 Hz: gain %.4f vs %.4f", got, reference)
	}
	mud := biquadGain(newPeaking(380, 8000, 1.0, -2.5), 380)
	reference = biquadGain(newPeaking(380, 8000, 1.0, -2.5), 1000)
	if mud >= reference {
		t.Fatalf("mud cut does not attenuate 380 Hz: %.4f vs %.4f", mud, reference)
	}
	presence := biquadGain(newPeaking(2600, 8000, 1.2, 4.0), 2600)
	reference = biquadGain(newPeaking(2600, 8000, 1.2, 4.0), 1000)
	if presence <= reference*1.2 {
		t.Fatalf("presence boost too weak: %.4f vs %.4f", presence, reference)
	}
}

func TestPolishSpeechSilence(t *testing.T) {
	pcm := make([]byte, 3200)
	out := PolishSpeech(pcm, 1.0)
	if len(out) != len(pcm) {
		t.Fatalf("silence length changed: %d != %d", len(out), len(pcm))
	}
	for i, b := range out {
		if b != 0 {
			t.Fatalf("silence sample %d = %d", i, b)
		}
	}
}

func TestSanitizeSpeech(t *testing.T) {
	cases := map[string]string{
		"**Привет**\n\nмир!":     "Привет мир!",
		"Список:\n- один\n- два": "Список: - один - два",
		"Ответ 😀 готов.":         "Ответ готов.",
		"`код` и _курсив_":       "код и курсив",
		"Цена 100 ₽ и 50%.":      "Цена 100 и 50%.",
	}
	for in, want := range cases {
		if got := sanitizeSpeech(in); got != want {
			t.Errorf("sanitizeSpeech(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncoderPreGain(t *testing.T) {
	if got := EncoderPreGain(0); math.Abs(got-EncoderInputScale) > 1e-9 {
		t.Fatalf("EncoderPreGain(0) = %v, want %v", got, EncoderInputScale)
	}
	if EncoderPreGain(-6) <= EncoderPreGain(0) {
		t.Fatal("negative gain_db must boost the encoder input")
	}
	if EncoderPreGain(6) >= EncoderPreGain(0) {
		t.Fatal("positive gain_db must attenuate the encoder input")
	}
}

func TestScalePCM16(t *testing.T) {
	pcm := []byte{0x10, 0x27, 0xf0, 0xd8}
	out := ScalePCM16(pcm, 0.5)
	if got := int16(binary.LittleEndian.Uint16(out)); got != 5000 {
		t.Fatalf("scaled positive = %d, want 5000", got)
	}
	if got := int16(binary.LittleEndian.Uint16(out[2:])); got != -5000 {
		t.Fatalf("scaled negative = %d, want -5000", got)
	}
	out = ScalePCM16(pcm, 10)
	if got := int16(binary.LittleEndian.Uint16(out)); got != 32767 {
		t.Fatalf("saturated positive = %d, want 32767", got)
	}
	if got := int16(binary.LittleEndian.Uint16(out[2:])); got != -32768 {
		t.Fatalf("saturated negative = %d, want -32768", got)
	}
}

func tonePCM(hz float64, rate, samples int, amp float64) []byte {
	pcm := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		v := int16(amp * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	return pcm
}

func rmsMiddle(pcm []byte) float64 {
	n := len(pcm) / 2
	var sum float64
	count := 0
	for i := n / 4; i < 3*n/4; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
		sum += v * v
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

func TestResampleKeepsPassbandFlat(t *testing.T) {
	const src, dst, hz, amp = 22050, 8000, 1000.0, 10000.0
	out := ResampleAntiAliased(tonePCM(hz, src, src, amp), src, dst)
	if len(out) != dst*2 {
		t.Fatalf("8 kHz output length = %d samples, want %d", len(out)/2, dst)
	}
	got := rmsMiddle(out)
	want := amp / math.Sqrt2
	if math.Abs(got-want)/want > 0.12 {
		t.Fatalf("1 kHz RMS after resample = %.0f, want %.0f (±12%%)", got, want)
	}
}

func TestResampleRejectsAliasing(t *testing.T) {
	const src, dst, amp = 22050, 8000, 10000.0
	out := ResampleAntiAliased(tonePCM(5000, src, src, amp), src, dst)
	if got, want := rmsMiddle(out), amp/math.Sqrt2; got > want*0.05 {
		t.Fatalf("5 kHz leaked through anti-alias filter: RMS %.0f (want < %.0f)", got, want*0.05)
	}
}

func TestParseVoiceName(t *testing.T) {
	cases := []struct {
		base, lang, quality, speaker string
	}{
		{"ru_RU-irina-medium", "ru_RU", "medium", "irina"},
		{"en_US-libritts-high", "en_US", "high", "libritts"},
		{"ru_RU-dict-medium_epoch3874", "ru_RU", "medium", "dict"},
		{"myvoice", "", "", "myvoice"},
	}
	for _, c := range cases {
		lang, quality, speaker := parseVoiceName(c.base)
		if lang != c.lang || quality != c.quality || speaker != c.speaker {
			t.Errorf("parseVoiceName(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.base, lang, quality, speaker, c.lang, c.quality, c.speaker)
		}
	}
}
