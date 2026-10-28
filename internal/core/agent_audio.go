package core

import (
	"encoding/binary"
	"math"
)

// ResampleLinear performs linear s16le resampling (RX path only).
func ResampleLinear(data []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate || len(data) < 2 {
		return data
	}
	nIn := len(data) / 2
	nOut := int(float64(nIn) * float64(dstRate) / float64(srcRate))
	ratio := float64(srcRate) / float64(dstRate)
	out := make([]byte, nOut*2)
	sample := func(i int) float64 {
		v := int16(binary.LittleEndian.Uint16(data[i*2:]))
		return float64(v)
	}
	for i := 0; i < nOut; i++ {
		pos := float64(i) * ratio
		i0 := int(pos)
		i1 := i0 + 1
		if i1 > nIn-1 {
			i1 = nIn - 1
		}
		frac := pos - float64(i0)
		v := sample(i0)*(1-frac) + sample(i1)*frac
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(v)))
	}
	return out
}

// ResampleAntiAliased downsamples s16le PCM using a Kaiser-windowed sinc
// low-pass filter and Catmull-Rom interpolation, preventing aliasing and
// keeping the passband flat before the AMBE+2 encoder.
func ResampleAntiAliased(data []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate || len(data) < 2 {
		return data
	}
	nIn := len(data) / 2
	if nIn == 0 {
		return data
	}
	nOut := int(float64(nIn) * float64(dstRate) / float64(srcRate))
	if nOut < 1 {
		return nil
	}
	x := make([]float64, nIn)
	for i := 0; i < nIn; i++ {
		x[i] = float64(int16(binary.LittleEndian.Uint16(data[i*2:])))
	}
	if dstRate < srcRate {
		// A longer kernel and a cutoff slightly below the new Nyquist give a
		// cleaner stopband, so encoder bits are not wasted on aliasing.
		taps := 96
		fCut := 0.45 * float64(dstRate) / float64(srcRate)
		m := 2*taps + 1
		kernel := make([]float64, m)
		var sum float64
		for i := 0; i < m; i++ {
			t := float64(i - taps)
			kernel[i] = 2 * fCut * sinc(2*fCut*t) * kaiser(i, m, 10.0)
			sum += kernel[i]
		}
		for i := range kernel {
			kernel[i] /= sum
		}
		x = convolveSame(x, kernel)
	}
	ratio := float64(srcRate) / float64(dstRate)
	out := make([]byte, nOut*2)
	for i := 0; i < nOut; i++ {
		v := cubicInterp(x, float64(i)*ratio)
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(math.Round(v))))
	}
	return out
}

// cubicInterp evaluates a Catmull-Rom spline at pos. Cubic interpolation has
// a much flatter response than linear interpolation, preserving presence in
// the 2..3.4 kHz range the vocoder passes through.
func cubicInterp(x []float64, pos float64) float64 {
	if len(x) == 0 {
		return 0
	}
	at := func(i int) float64 {
		if i < 0 {
			i = 0
		} else if i >= len(x) {
			i = len(x) - 1
		}
		return x[i]
	}
	i := int(math.Floor(pos))
	f := pos - float64(i)
	xm1, x0, x1, x2 := at(i-1), at(i), at(i+1), at(i+2)
	a0 := -0.5*xm1 + 1.5*x0 - 1.5*x1 + 0.5*x2
	a1 := xm1 - 2.5*x0 + 2*x1 - 0.5*x2
	a2 := -0.5*xm1 + 0.5*x1
	return ((a0*f+a1)*f+a2)*f + x0
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	x *= math.Pi
	return math.Sin(x) / x
}

func kaiser(n, m int, beta float64) float64 {
	if m <= 1 {
		return 1
	}
	r := 2*float64(n)/float64(m-1) - 1
	return besselI0(beta*math.Sqrt(1-r*r)) / besselI0(beta)
}

func besselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-12*sum {
			break
		}
	}
	return sum
}

func convolveSame(x, kernel []float64) []float64 {
	full := make([]float64, len(x)+len(kernel)-1)
	for i, xv := range x {
		if xv == 0 {
			continue
		}
		for j, kv := range kernel {
			full[i+j] += xv * kv
		}
	}
	start := (len(full) - len(x)) / 2
	return full[start : start+len(x)]
}

// S16LEToF32 converts s16le samples to [-1,1] floats.
func S16LEToF32(data []byte) []float64 {
	n := len(data) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = float64(int16(binary.LittleEndian.Uint16(data[i*2:]))) / 32768.0
	}
	return out
}

// F32ToS16LE converts [-1,1] floats to clipped s16le bytes.
func F32ToS16LE(samples []float64) []byte {
	out := make([]byte, len(samples)*2)
	for i, v := range samples {
		if v > 1 {
			v = 1
		}
		if v < -1 {
			v = -1
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(v*32767)))
	}
	return out
}

type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *biquad) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

func newBiquad(b0, b1, b2, a0, a1, a2 float64) *biquad {
	return &biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0}
}

func newHighPass(hz, rate, q float64) *biquad {
	w0 := 2 * math.Pi * hz / rate
	alpha := math.Sin(w0) / (2 * q)
	cosw := math.Cos(w0)
	return newBiquad((1+cosw)/2, -(1 + cosw), (1+cosw)/2, 1+alpha, -2*cosw, 1-alpha)
}

func newLowPass(hz, rate, q float64) *biquad {
	w0 := 2 * math.Pi * hz / rate
	alpha := math.Sin(w0) / (2 * q)
	cosw := math.Cos(w0)
	return newBiquad((1-cosw)/2, 1-cosw, (1-cosw)/2, 1+alpha, -2*cosw, 1-alpha)
}

func newPeaking(hz, rate, q, gainDB float64) *biquad {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * hz / rate
	alpha := math.Sin(w0) / (2 * q)
	cosw := math.Cos(w0)
	return newBiquad(1+alpha*A, -2*cosw, 1-alpha*A, 1+alpha/A, -2*cosw, 1-alpha/A)
}

// PolishSpeech conditions 8 kHz s16le TTS audio for the AMBE+2 vocoder:
// it band-limits speech to the radio passband, lifts presence, trims excess
// silence, evens out loudness, limits peaks and fades the edges so the radio
// hears clear, consistent speech.
func PolishSpeech(pcm []byte, volume float64) []byte {
	if len(pcm) < 2 {
		return pcm
	}
	if volume <= 0 {
		volume = 1.0
	}
	x := S16LEToF32(pcm)

	// AMBE+2 only carries roughly 300..3400 Hz. Removing everything outside
	// that band keeps the vocoder from wasting bits on rumble and hiss.
	hp := newHighPass(300, 8000, 0.707)
	lp := newLowPass(3400, 8000, 0.707)
	mud := newPeaking(380, 8000, 1.0, -2.5)
	presence := newPeaking(2600, 8000, 1.2, 4.0)
	for i := range x {
		v := hp.process(x[i])
		v = lp.process(v)
		v = mud.process(v)
		x[i] = presence.process(v)
	}

	peak := 0.0
	for _, v := range x {
		if p := math.Abs(v); p > peak {
			peak = p
		}
	}
	if peak < 1e-4 {
		return make([]byte, len(pcm))
	}
	for i := range x {
		x[i] *= 0.7 / peak
	}
	peak = 0.7

	// Trim silence but keep a short lead-in and a natural tail.
	thr := peak * 0.01
	start, end := 0, len(x)-1
	for start < len(x) && math.Abs(x[start]) < thr {
		start++
	}
	for end > start && math.Abs(x[end]) < thr {
		end--
	}
	start -= 50 * 8
	if start < 0 {
		start = 0
	}
	end += 80 * 8
	if end >= len(x) {
		end = len(x) - 1
	}
	if end > start {
		x = x[start : end+1]
	}

	// Gentle compressor: consonants stay audible without pumping.
	// 8 ms attack, 120 ms release, 2:1 above roughly -9 dBFS.
	const (
		threshold = 0.35
		ratio     = 2.0
	)
	attack := math.Exp(-1.0 / (0.008 * 8000))
	release := math.Exp(-1.0 / (0.120 * 8000))
	env := 0.0
	for i, v := range x {
		mag := math.Abs(v)
		if mag > env {
			env = attack*env + (1-attack)*mag
		} else {
			env = release*env + (1-release)*mag
		}
		if env > threshold {
			x[i] = v * math.Pow(env/threshold, 1.0/ratio-1.0)
		}
	}

	// Normalise to a consistent peak. 0.88 leaves headroom the AMBE+2 encoder
	// needs; pushing TTS to full scale only adds vocoder distortion.
	peak = 0.0
	for _, v := range x {
		if p := math.Abs(v); p > peak {
			peak = p
		}
	}
	if peak > 0 {
		target := 0.88 * volume
		if target > 0.98 {
			target = 0.98
		}
		g := target / peak
		for i := range x {
			x[i] *= g
		}
	}

	// 5 ms fades prevent clicks at the start and end of the transmission.
	fade := 5 * 8
	if fade*2 > len(x) {
		fade = len(x) / 2
	}
	for i := 0; i < fade; i++ {
		k := float64(i) / float64(fade)
		x[i] *= k
		x[len(x)-1-i] *= k
	}
	return F32ToS16LE(x)
}

// EncoderInputScale is the fixed pre-gain applied before the AMBE+2 encoder.
// The IMBE-based encoder expects a much lower input level than normalized TTS
// audio; without this the decoded signal is loud, clipped and hard to read.
const EncoderInputScale = 0.06

// EncoderPreGain returns the linear input scale for the given tx_gain_db.
// Positive gainDB attenuates, negative values boost (legacy config semantics).
func EncoderPreGain(gainDB int) float64 {
	return EncoderInputScale * math.Pow(10, -float64(gainDB)/20)
}

// ScalePCM16 multiplies s16le samples by scale with saturation.
func ScalePCM16(data []byte, scale float64) []byte {
	if scale == 1 || len(data) < 2 {
		return data
	}
	out := make([]byte, len(data))
	for i := 0; i+1 < len(data); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(data[i:]))) * scale
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(out[i:], uint16(int16(v)))
	}
	return out
}
