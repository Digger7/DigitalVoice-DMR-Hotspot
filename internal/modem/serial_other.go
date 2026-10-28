//go:build !darwin && !linux && !windows

package modem

import "errors"

type SerialPort struct{ device string }

func NewSerialTransport(device string, _ int) *SerialPort { return &SerialPort{device: device} }
func (p *SerialPort) Device() string                      { return p.device }
func (p *SerialPort) SetSink(Sink)                        {}
func (p *SerialPort) Open() error {
	return errors.New("serial transport not implemented on this platform")
}
func (p *SerialPort) Write([]byte) (int, error) {
	return 0, errors.New("serial transport not implemented")
}
func (p *SerialPort) Close() error { return nil }
func ListSerialPorts() []string    { return nil }
