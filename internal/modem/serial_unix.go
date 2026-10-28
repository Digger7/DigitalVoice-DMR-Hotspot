//go:build darwin || linux

package modem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// SerialPort is a Unix TTY transport with a background reader goroutine.
type SerialPort struct {
	device string
	baud   int
	file   *os.File
	fd     int

	mu    sync.Mutex
	sink  Sink
	stop  chan struct{}
	done  chan struct{}
	open  bool
	write sync.Mutex
}

func NewSerialTransport(device string, baud int) *SerialPort {
	return &SerialPort{device: device, baud: baud}
}

func (p *SerialPort) Device() string { return p.device }

func (p *SerialPort) SetSink(s Sink) {
	p.mu.Lock()
	p.sink = s
	p.mu.Unlock()
}

func (p *SerialPort) Open() error {
	f, err := os.OpenFile(p.device, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0o666)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", p.device, err)
	}
	fd := int(f.Fd())
	if err := configurePort(fd, p.baud); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot configure %s: %w", p.device, err)
	}
	_ = unix.IoctlSetInt(fd, unix.TIOCEXCL, 1)

	p.mu.Lock()
	p.file = f
	p.fd = fd
	p.open = true
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	stop, done := p.stop, p.done
	p.mu.Unlock()

	go p.readLoop(fd, stop, done)
	return nil
}

func (p *SerialPort) readLoop(fd int, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := unix.Read(fd, buf)
		if n > 0 {
			p.mu.Lock()
			sink := p.sink
			p.mu.Unlock()
			if sink != nil {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				sink.OnData(chunk)
			}
			continue
		}
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			select {
			case <-stop:
				return
			default:
			}
			p.mu.Lock()
			sink := p.sink
			p.mu.Unlock()
			if sink != nil {
				sink.OnError(err.Error())
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (p *SerialPort) Write(data []byte) (int, error) {
	p.write.Lock()
	defer p.write.Unlock()
	p.mu.Lock()
	fd, open := p.fd, p.open
	p.mu.Unlock()
	if !open {
		return 0, errors.New("serial port is not open")
	}
	total := 0
	for total < len(data) {
		n, err := unix.Write(fd, data[total:])
		if n > 0 {
			total += n
		}
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
				time.Sleep(time.Millisecond)
				continue
			}
			return total, fmt.Errorf("write failed: %w", err)
		}
	}
	return total, nil
}

func (p *SerialPort) Close() error {
	p.mu.Lock()
	if !p.open {
		p.mu.Unlock()
		return nil
	}
	p.open = false
	stop, done, f := p.stop, p.done, p.file
	p.mu.Unlock()

	close(stop)
	_ = f.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	return nil
}

// ListSerialPorts returns candidate radio serial devices.
func ListSerialPorts() []string {
	var patterns []string
	switch {
	case fileExists("/dev/cu."):
		patterns = []string{"/dev/cu.*"}
	default:
		patterns = []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/serial/by-id/*"}
	}
	seen := map[string]bool{}
	var out []string
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
