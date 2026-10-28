package core

import (
	"sync"
	"testing"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/modem"
	"dmr-hotspot/internal/protocol"
)

type fakeTransport struct {
	mu      sync.Mutex
	sink    modem.Sink
	writes  [][]byte
	started bool
}

func (f *fakeTransport) Device() string { return "fake" }
func (f *fakeTransport) Open() error    { f.started = true; return nil }
func (f *fakeTransport) Close() error   { return nil }
func (f *fakeTransport) SetSink(s modem.Sink) {
	f.mu.Lock()
	f.sink = s
	f.mu.Unlock()
}

func frame(payload ...byte) []byte {
	total := len(payload) + 2
	out := make([]byte, 0, total)
	out = append(out, protocol.MMDVMFrameStart, byte(total))
	out = append(out, payload...)
	return out
}

func (f *fakeTransport) Write(data []byte) (int, error) {
	cp := append([]byte(nil), data...)
	f.mu.Lock()
	f.writes = append(f.writes, cp)
	sink := f.sink
	f.mu.Unlock()

	if len(data) < 3 {
		return len(data), nil
	}
	typ := data[2]
	switch typ {
	case protocol.MMDVMGetVersion:
		sink.OnData(frame(protocol.MMDVMGetVersion, 2, 0x02, 0, 0, 'x', 0))
	case protocol.MMDVMSetConfig, protocol.MMDVMSetFreq, protocol.MMDVMSetMode:
		sink.OnData(frame(protocol.MMDVMAck))
	case protocol.MMDVMGetStatus:
		sink.OnData(frame(protocol.MMDVMGetStatus, protocol.ModeDMR, 0, 0, 0, 15, 15))
	}
	return len(data), nil
}

func (f *fakeTransport) countType(t byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, w := range f.writes {
		if len(w) >= 3 && w[2] == t {
			n++
		}
	}
	return n
}

func TestEchoReplaysThroughCore(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = "echo"
	cfg.Modem.Port = "fake"
	cfg.RF.TXDCOffset, cfg.RF.RXDCOffset = 0, 0

	c := NewCore(cfg)
	ft := &fakeTransport{}
	c.SetPortOverride(ft)
	c.echo = NewEchoRepeater(cfg.RF.ColorCode, 100, 30*time.Millisecond)
	c.echo.SetLastHeardCallback(c.onLastHeard)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	deadline := time.Now().Add(4 * time.Second)
	for c.Status().State != "RUNNING" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if c.Status().State != "RUNNING" {
		t.Fatalf("core did not start: %+v", c.Status())
	}

	m := c.modem
	for i := 0; i < 5; i++ {
		meta := byte(0x20)
		if i == 0 {
			meta = 0x41
		}
		payload := []byte{meta}
		payload = append(payload, burstWith(byte(i))...)
		payload = append(payload, 0, 0)
		m.OnData(frame(append([]byte{protocol.MMDVMDMRData2}, payload...)...))
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	if got := ft.countType(protocol.MMDVMDMRData2); got == 0 {
		t.Fatalf("echo did not replay: writes=%d stats=%+v", len(ft.writes), c.echo.Stats())
	} else {
		t.Logf("replayed %d bursts", got)
	}
}
