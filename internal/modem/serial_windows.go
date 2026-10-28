//go:build windows

package modem

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// DCB flag bits packed into DCB.Flags: fBinary, DTR_CONTROL_ENABLE,
	// RTS_CONTROL_ENABLE. No flow control - the modem protocol does its own
	// packet framing.
	dcbFlagBinary    = 0x00000001
	dcbFlagDtrEnable = 0x00000010
	dcbFlagRtsEnable = 0x00001000

	waitObject0 = 0
	waitTimeout = 258 // WAIT_TIMEOUT

	purgeAll = windows.PURGE_TXABORT | windows.PURGE_RXABORT |
		windows.PURGE_TXCLEAR | windows.PURGE_RXCLEAR

	maxSerialChunk = 4096
	writeTimeout   = 5 * time.Second
)

var comNameRe = regexp.MustCompile(`(?i)^COM(\d+)$`)

// SerialPort is a Windows COM-port transport with a background reader
// goroutine. Uses overlapped I/O so Close can cancel a pending read.
type SerialPort struct {
	device string
	baud   int

	mu     sync.Mutex
	sink   Sink
	handle windows.Handle
	rEvent windows.Handle
	wEvent windows.Handle
	open   bool

	stop    chan struct{}
	done    chan struct{}
	writeMu sync.Mutex
	writers sync.WaitGroup
}

func NewSerialTransport(device string, baud int) *SerialPort {
	if baud <= 0 {
		baud = 115200
	}
	return &SerialPort{device: device, baud: baud}
}

func (p *SerialPort) Device() string { return p.device }

func (p *SerialPort) SetSink(s Sink) {
	p.mu.Lock()
	p.sink = s
	p.mu.Unlock()
}

func (p *SerialPort) Open() error {
	name := p.device
	if upper := strings.ToUpper(name); !strings.HasPrefix(upper, `\\.\`) &&
		comNameRe.MatchString(upper) {
		name = `\\.\` + name
	}
	p16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", p.device, err)
	}
	h, err := windows.CreateFile(p16, windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", p.device, err)
	}
	if err := configureSerial(h, p.baud); err != nil {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("cannot configure %s: %w", p.device, err)
	}
	rEv, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("cannot open %s: %w", p.device, err)
	}
	wEv, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(rEv)
		_ = windows.CloseHandle(h)
		return fmt.Errorf("cannot open %s: %w", p.device, err)
	}

	p.mu.Lock()
	p.handle, p.rEvent, p.wEvent = h, rEv, wEv
	p.open = true
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	stop, done := p.stop, p.done
	p.mu.Unlock()

	go p.readLoop(h, rEv, stop, done)
	return nil
}

func configureSerial(h windows.Handle, baud int) error {
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(h, &dcb); err != nil {
		return err
	}
	dcb.BaudRate = uint32(baud)
	dcb.Flags = dcbFlagBinary | dcbFlagDtrEnable | dcbFlagRtsEnable
	dcb.ByteSize = 8
	dcb.Parity = 0 // no parity
	dcb.StopBits = 0
	dcb.XonLim = 2048
	dcb.XoffLim = 512
	if err := windows.SetCommState(h, &dcb); err != nil {
		return err
	}
	timeouts := windows.CommTimeouts{
		ReadIntervalTimeout:         0xFFFFFFFF,
		ReadTotalTimeoutMultiplier:  0,
		ReadTotalTimeoutConstant:    0,
		WriteTotalTimeoutMultiplier: 0,
		WriteTotalTimeoutConstant:   0,
	}
	if err := windows.SetCommTimeouts(h, &timeouts); err != nil {
		return err
	}
	_ = windows.SetupComm(h, maxSerialChunk, maxSerialChunk)
	_ = windows.PurgeComm(h, purgeAll)
	return nil
}

func (p *SerialPort) readLoop(h, ev windows.Handle, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, maxSerialChunk)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, aborted, err := readOnce(h, ev, buf, stop)
		if aborted {
			return
		}
		if err != nil {
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
		if n > 0 {
			p.mu.Lock()
			sink := p.sink
			p.mu.Unlock()
			if sink != nil {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				sink.OnData(chunk)
			}
		}
	}
}

// readOnce performs one overlapped read. aborted is true when the operation
// was cancelled or the port is shutting down.
func readOnce(h, ev windows.Handle, buf []byte, stop <-chan struct{}) (n uint32, aborted bool, err error) {
	ov := &windows.Overlapped{HEvent: ev}
	rerr := windows.ReadFile(h, buf, &n, ov)
	if errors.Is(rerr, windows.ERROR_IO_PENDING) {
		for {
			s, werr := windows.WaitForSingleObject(ev, 100)
			if werr == nil && s == waitObject0 {
				if err = windows.GetOverlappedResult(h, ov, &n, false); err != nil {
					if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
						return 0, true, nil
					}
					return 0, false, err
				}
				return n, false, nil
			}
			select {
			case <-stop:
				_ = windows.CancelIoEx(h, ov)
				_, _ = windows.WaitForSingleObject(ev, 1000)
				return 0, true, nil
			default:
			}
			if werr != nil {
				return 0, false, werr
			}
		}
	}
	if rerr != nil {
		return 0, false, rerr
	}
	return n, false, nil
}

func (p *SerialPort) Write(data []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.mu.Lock()
	h, ev, open := p.handle, p.wEvent, p.open
	stop := p.stop
	p.mu.Unlock()
	if !open {
		return 0, errors.New("serial port is not open")
	}

	p.writers.Add(1)
	defer p.writers.Done()

	total := 0
	for total < len(data) {
		select {
		case <-stop:
			return total, errors.New("serial port is closing")
		default:
		}
		chunk := data[total:]
		if len(chunk) > maxSerialChunk {
			chunk = chunk[:maxSerialChunk]
		}
		n, err := writeOnce(h, ev, chunk, stop, writeTimeout)
		total += int(n)
		if err != nil {
			return total, fmt.Errorf("write failed: %w", err)
		}
		if n == 0 {
			return total, errors.New("write failed: no progress")
		}
	}
	return total, nil
}

func writeOnce(h, ev windows.Handle, chunk []byte, stop <-chan struct{}, timeout time.Duration) (n uint32, err error) {
	ov := &windows.Overlapped{HEvent: ev}
	werr := windows.WriteFile(h, chunk, &n, ov)
	if !errors.Is(werr, windows.ERROR_IO_PENDING) {
		if werr == nil {
			return n, nil
		}
		return 0, werr
	}
	deadline := time.Now().Add(timeout)
	for {
		s, werr2 := windows.WaitForSingleObject(ev, 100)
		if werr2 == nil && s == waitObject0 {
			if err = windows.GetOverlappedResult(h, ov, &n, false); err != nil {
				return 0, err
			}
			return n, nil
		}
		select {
		case <-stop:
			_ = windows.CancelIoEx(h, ov)
			_, _ = windows.WaitForSingleObject(ev, 1000)
			return 0, errors.New("serial port is closing")
		default:
		}
		if werr2 != nil {
			return 0, werr2
		}
		if time.Now().After(deadline) {
			_ = windows.CancelIoEx(h, ov)
			_, _ = windows.WaitForSingleObject(ev, 1000)
			return 0, errors.New("write timeout")
		}
	}
}

func (p *SerialPort) Close() error {
	p.mu.Lock()
	if !p.open {
		p.mu.Unlock()
		return nil
	}
	p.open = false
	h, rEv, wEv, stop, done := p.handle, p.rEvent, p.wEvent, p.stop, p.done
	p.mu.Unlock()

	close(stop)
	_ = windows.CancelIoEx(h, nil)
	p.writers.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	_ = windows.CloseHandle(rEv)
	_ = windows.CloseHandle(wEv)
	_ = windows.CloseHandle(h)
	return nil
}

// ListSerialPorts returns available COM ports, sorted numerically (COM2
// before COM10).
func ListSerialPorts() []string {
	type comPort struct {
		name string
		num  int
	}
	var ports []comPort
	for size := uint32(4096); size <= 1<<20; size *= 2 {
		buf := make([]uint16, size)
		n, err := windows.QueryDosDevice(nil, &buf[0], size)
		if err != nil {
			if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
				continue
			}
			break
		}
		start := 0
		for i, c := range buf[:n] {
			if c != 0 {
				continue
			}
			if i == start {
				break
			}
			name := windows.UTF16ToString(buf[start:i])
			start = i + 1
			if m := comNameRe.FindStringSubmatch(name); m != nil {
				num, _ := strconv.Atoi(m[1])
				ports = append(ports, comPort{name: strings.ToUpper(name), num: num})
			}
		}
		break
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].num != ports[j].num {
			return ports[i].num < ports[j].num
		}
		return ports[i].name < ports[j].name
	})
	out := make([]string, len(ports))
	for i, pc := range ports {
		out[i] = pc.name
	}
	return out
}
