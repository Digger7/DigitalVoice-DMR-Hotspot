//go:build !darwin || !cgo

package dmr

import "errors"

const (
	AMBEBytes      = 9
	PCMSamples     = 160
	AMBESampleRate = 8000
)

var ErrVocoderUnavailable = errors.New("libopendmr is only available on darwin with cgo enabled")

type Decoder struct{}

func NewDecoder() (*Decoder, error)                   { return nil, ErrVocoderUnavailable }
func (d *Decoder) Close()                             {}
func (d *Decoder) Reset()                             {}
func (d *Decoder) Decode([]byte) ([]byte, int, error) { return nil, 0, ErrVocoderUnavailable }

type Encoder struct{}

func NewEncoder() (*Encoder, error)              { return nil, ErrVocoderUnavailable }
func (e *Encoder) Close()                        {}
func (e *Encoder) Reset()                        {}
func (e *Encoder) SetGain(int)                   {}
func (e *Encoder) Encode([]byte) ([]byte, error) { return nil, ErrVocoderUnavailable }

func OpenDMRVersion() string { return "unavailable" }
