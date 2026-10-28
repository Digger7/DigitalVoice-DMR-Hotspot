//go:build darwin && cgo

package dmr

/*
#cgo LDFLAGS: -L${SRCDIR} -lopendmr -Wl,-rpath,${SRCDIR}
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>

typedef void* opendmr_decoder_t;
typedef void* opendmr_encoder_t;

extern opendmr_decoder_t opendmr_decoder_create(void);
extern void opendmr_decoder_destroy(opendmr_decoder_t);
extern void opendmr_decoder_reset(opendmr_decoder_t);
extern bool opendmr_decode(opendmr_decoder_t, uint8_t*, int16_t*, int*);

extern opendmr_encoder_t opendmr_encoder_create(void);
extern void opendmr_encoder_destroy(opendmr_encoder_t);
extern void opendmr_encoder_reset(opendmr_encoder_t);
extern void opendmr_encoder_set_gain(opendmr_encoder_t, int);
extern bool opendmr_encode(opendmr_encoder_t, int16_t*, uint8_t*);

extern const char* opendmr_version(void);
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
)

const (
	AMBEBytes      = 9
	PCMSamples     = 160
	AMBESampleRate = 8000
)

var ErrVocoderUnavailable = errors.New("libopendmr unavailable")

type Decoder struct{ ptr C.opendmr_decoder_t }

func NewDecoder() (*Decoder, error) {
	ptr := C.opendmr_decoder_create()
	if ptr == nil {
		return nil, errors.New("opendmr_decoder_create failed")
	}
	d := &Decoder{ptr: ptr}
	runtime.SetFinalizer(d, (*Decoder).Close)
	return d, nil
}

func (d *Decoder) Close() {
	if d.ptr != nil {
		C.opendmr_decoder_destroy(d.ptr)
		d.ptr = nil
	}
}

func (d *Decoder) Reset() {
	if d.ptr != nil {
		C.opendmr_decoder_reset(d.ptr)
	}
}

// Decode decodes one 9-byte AMBE+2 frame into 320 bytes of s16le PCM.
func (d *Decoder) Decode(ambe []byte) ([]byte, int, error) {
	if len(ambe) != AMBEBytes {
		return nil, 0, fmt.Errorf("frame must be %d bytes", AMBEBytes)
	}
	var in [AMBEBytes]C.uint8_t
	for i, b := range ambe {
		in[i] = C.uint8_t(b)
	}
	var pcm [PCMSamples]C.int16_t
	var errs C.int
	ok := C.opendmr_decode(d.ptr, &in[0], &pcm[0], &errs)
	if !ok {
		return nil, 0, errors.New("opendmr_decode failed")
	}
	out := make([]byte, PCMSamples*2)
	for i, s := range pcm {
		v := uint16(s)
		out[i*2] = byte(v & 0xFF)
		out[i*2+1] = byte(v >> 8)
	}
	return out, int(errs), nil
}

type Encoder struct{ ptr C.opendmr_encoder_t }

func NewEncoder() (*Encoder, error) {
	ptr := C.opendmr_encoder_create()
	if ptr == nil {
		return nil, errors.New("opendmr_encoder_create failed")
	}
	e := &Encoder{ptr: ptr}
	runtime.SetFinalizer(e, (*Encoder).Close)
	return e, nil
}

func (e *Encoder) Close() {
	if e.ptr != nil {
		C.opendmr_encoder_destroy(e.ptr)
		e.ptr = nil
	}
}

func (e *Encoder) Reset() {
	if e.ptr != nil {
		C.opendmr_encoder_reset(e.ptr)
	}
}

// SetGain adjusts the encoder's post-quantisation gain offset in dB.
// TX level is controlled earlier by core.EncoderPreGain (PCM pre-scaling);
// leave this at 0 unless you need to trim the AMBE gain index directly.
func (e *Encoder) SetGain(gainDB int) {
	if e.ptr != nil {
		C.opendmr_encoder_set_gain(e.ptr, C.int(gainDB))
	}
}

// Encode encodes 320 bytes (160 samples) of s16le PCM into a 9-byte frame.
func (e *Encoder) Encode(pcm []byte) ([]byte, error) {
	if len(pcm) != PCMSamples*2 {
		return nil, fmt.Errorf("pcm must be %d bytes (160 samples)", PCMSamples*2)
	}
	var samples [PCMSamples]C.int16_t
	for i := 0; i < PCMSamples; i++ {
		samples[i] = C.int16_t(uint16(pcm[i*2]) | uint16(pcm[i*2+1])<<8)
	}
	var out [AMBEBytes]C.uint8_t
	ok := C.opendmr_encode(e.ptr, &samples[0], &out[0])
	if !ok {
		return nil, errors.New("opendmr_encode failed")
	}
	b := make([]byte, AMBEBytes)
	for i := 0; i < AMBEBytes; i++ {
		b[i] = byte(out[i])
	}
	runtime.KeepAlive(e)
	return b, nil
}

func OpenDMRVersion() string {
	return C.GoString(C.opendmr_version())
}
