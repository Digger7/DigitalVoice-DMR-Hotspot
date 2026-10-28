package modem

// Transport is what the modem needs from a serial link. The desktop build
// implements it with a Unix TTY; tests can provide an in-memory pipe.
type Transport interface {
	Device() string
	Open() error
	Write(data []byte) (int, error)
	Close() error
	SetSink(Sink)
}

// Sink receives bytes from the transport. Both callbacks must be safe to call
// from the reader goroutine.
type Sink interface {
	OnData([]byte)
	OnError(string)
}

// DetectTransport returns the default transport for the current platform.
func DetectTransport(device string) Transport { return NewSerialTransport(device, 115200) }

// DetectTransportBaud is DetectTransport with an explicit baud rate.
func DetectTransportBaud(device string, baud int) Transport {
	return NewSerialTransport(device, baud)
}
