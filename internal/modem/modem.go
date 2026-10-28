package modem

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/protocol"
)

// OpenGD77 metadata flags for DMR RX frames.
const (
	MetaDataSync  = 0x40
	MetaAudioSync = 0x20
)

var log = logx.GetLogger("modem")

type ModemError struct{ msg string }

func (e *ModemError) Error() string { return e.msg }

func NewModemError(format string, args ...any) error {
	return &ModemError{msg: fmt.Sprintf(format, args...)}
}

type Frame struct {
	Type byte
	Data []byte
}

type Modem struct {
	port Transport

	duplex    bool
	rxInvert  bool
	txInvert  bool
	pttInvert bool
	txDelay   int
	dmrDelay  int
	txDC      int
	rxDC      int
	debug     bool

	mu      sync.Mutex
	buffer  []byte
	state   int
	length  int
	offset  int
	frameTy byte

	ProtocolVersion int
	Capabilities1   int
	Capabilities2   int
	Description     string

	mode    atomic.Int32
	tx      atomic.Bool
	cd      atomic.Bool
	lockout atomic.Bool
	space1  atomic.Int32
	space2  atomic.Int32

	statusSeen atomic.Bool

	frames chan Frame
	txMu   sync.Mutex

	errorMu sync.Mutex
	errorCb func(string)
}

// NewModem creates a modem bound to a transport.
func NewModem(port Transport, duplex, rxInvert, txInvert, pttInvert bool,
	txDelay, dmrDelay, txDC, rxDC int, debug bool) *Modem {
	return &Modem{
		port: port, duplex: duplex, rxInvert: rxInvert, txInvert: txInvert,
		pttInvert: pttInvert, txDelay: txDelay, dmrDelay: dmrDelay,
		txDC: txDC, rxDC: rxDC, debug: debug,
		frames: make(chan Frame, 2048),
	}
}

func (m *Modem) OnError(err string) {
	m.errorMu.Lock()
	cb := m.errorCb
	m.errorMu.Unlock()
	if cb != nil {
		cb(err)
	} else {
		log.Errorf("Serial error: %s", err)
	}
}

func (m *Modem) SetErrorCallback(cb func(string)) {
	m.errorMu.Lock()
	m.errorCb = cb
	m.errorMu.Unlock()
}

func (m *Modem) HasDMR() bool { return m.Capabilities1&protocol.Cap1DMR == protocol.Cap1DMR }
func (m *Modem) Mode() byte   { return byte(m.mode.Load()) }
func (m *Modem) TX() bool     { return m.tx.Load() }
func (m *Modem) CD() bool     { return m.cd.Load() }
func (m *Modem) DMRSpace2() int {
	v := int(m.space2.Load())
	return v
}

// HasStatus reports whether at least one GET_STATUS response has been parsed.
// Before that, a DMRSpace value of zero means "unknown", not "full".
func (m *Modem) HasStatus() bool { return m.statusSeen.Load() }

// NoteDMR2Sent accounts for bursts already handed to the modem since the last
// GET_STATUS response. The radio consumes one frame per air slot; decrementing
// locally between status polls keeps us from overflowing its ring buffer when
// the reported free space is stale.
func (m *Modem) NoteDMR2Sent(n int) {
	for i := 0; i < n; i++ {
		for {
			v := m.space2.Load()
			if v <= 0 {
				return
			}
			if m.space2.CompareAndSwap(v, v-1) {
				break
			}
		}
	}
}

// OnData feeds raw serial bytes into the MMDVM frame parser.
func (m *Modem) OnData(data []byte) {
	for _, b := range data {
		m.feedByte(b)
	}
}

func (m *Modem) feedByte(b byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case 0: // START
		if b == protocol.MMDVMFrameStart {
			m.buffer = []byte{b}
			m.state = 1
			m.length = 1
		}
	case 1: // LENGTH1
		m.buffer = append(m.buffer, b)
		m.length = int(b)
		m.offset = 2
		if b == 0 {
			m.state = 2
		} else {
			m.state = 3
		}
	case 2: // LENGTH2
		m.buffer = append(m.buffer, b)
		m.length = int(b) + 255
		m.offset = 3
		m.state = 3
	case 3: // TYPE
		m.frameTy = b
		m.buffer = append(m.buffer, b)
		m.offset++
		if m.offset >= m.length {
			m.processFrameLocked()
			m.state = 0
		} else {
			m.state = 4
		}
	default: // DATA
		m.buffer = append(m.buffer, b)
		m.offset++
		if m.offset >= m.length {
			m.processFrameLocked()
			m.state = 0
		}
	}
}

// processFrameLocked is called with m.mu held.
func (m *Modem) processFrameLocked() {
	start := 3
	if len(m.buffer) > 2 && m.buffer[1] == 0 {
		start = 4
	}
	payload := []byte{}
	if start <= len(m.buffer) {
		payload = append(payload, m.buffer[start:]...)
	}
	if m.frameTy == protocol.MMDVMGetStatus {
		m.parseStatus(payload)
	}
	if m.debug {
		log.Debugf("RX frame 0x%02X len=%d %x", m.frameTy, len(m.buffer), payload)
	}
	select {
	case m.frames <- Frame{Type: m.frameTy, Data: payload}:
	default:
	}
}

func (m *Modem) parseStatus(payload []byte) {
	// OpenGD77 protocol v1 status: [0]=caps [1]=mode [2]=flags.
	// flags: bit0=tx bit2=rxOverflow bit3=txOverflow bit4=lockout bit6=cd
	if len(payload) < 3 {
		return
	}
	m.mode.Store(int32(payload[1]))
	flags := payload[2]
	m.tx.Store(flags&0x01 != 0)
	m.cd.Store(flags&0x40 != 0)
	m.lockout.Store(flags&0x10 != 0)
	if len(payload) >= 6 {
		m.space1.Store(int32(payload[4]))
		m.space2.Store(int32(payload[5]))
	}
	m.statusSeen.Store(true)
}

// ReadFrame returns the next parsed frame or nil on timeout.
func (m *Modem) ReadFrame(timeout time.Duration) *Frame {
	if timeout <= 0 {
		select {
		case f := <-m.frames:
			return &f
		default:
			return nil
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case f := <-m.frames:
		return &f
	case <-timer.C:
		return nil
	}
}

func (m *Modem) send(frameType byte, payload []byte) error {
	total := len(payload) + 3
	if total > 255 {
		return NewModemError("frame too long (%d)", total)
	}
	frame := make([]byte, 0, total)
	frame = append(frame, protocol.MMDVMFrameStart, byte(total), frameType)
	frame = append(frame, payload...)
	if m.debug {
		log.Debugf("TX frame 0x%02X len=%d %x", frameType, total, payload)
	}
	m.txMu.Lock()
	_, err := m.port.Write(frame)
	m.txMu.Unlock()
	return err
}

func (m *Modem) waitResponse(expect map[byte]bool, timeout time.Duration) *Frame {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f := m.ReadFrame(50 * time.Millisecond)
		if f != nil && expect[f.Type] {
			return f
		}
	}
	return nil
}

func (m *Modem) GetVersion(retries int, settle float64) bool {
	if settle > 0 {
		log.Infof("Waiting %.1fs for the modem to settle", settle)
		time.Sleep(time.Duration(settle * float64(time.Second)))
	}
	for i := 0; i < retries; i++ {
		_ = m.send(protocol.MMDVMGetVersion, nil)
		f := m.waitResponse(map[byte]bool{protocol.MMDVMGetVersion: true}, 3*time.Second)
		if f != nil {
			m.parseVersion(f.Data)
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Errorf("Unable to read the firmware version")
	return false
}

func (m *Modem) parseVersion(payload []byte) {
	if len(payload) < 1 {
		return
	}
	m.ProtocolVersion = int(payload[0])
	if m.ProtocolVersion == 1 {
		m.Capabilities1 = protocol.Cap1DStar | protocol.Cap1DMR | protocol.Cap1YSF | protocol.Cap1P25 | protocol.Cap1NXDN
		m.Capabilities2 = protocol.Cap2POCSAG
		m.Description = latin1(payload[1:])
	} else if m.ProtocolVersion == 2 && len(payload) >= 4 {
		m.Capabilities1 = int(payload[1])
		m.Capabilities2 = int(payload[2])
		m.Description = latin1(payload[4:])
	} else {
		m.Description = latin1(payload[1:])
	}
	log.Infof("MMDVM protocol version %d, DMR supported=%v ident='%s'",
		m.ProtocolVersion, m.HasDMR(), m.Description)
}

func latin1(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return strings.TrimRight(sb.String(), "\x00 ")
}

func (m *Modem) SetConfig(colorCode int, dmrEnabled bool) bool {
	if m.ProtocolVersion == 2 {
		return m.setConfigV2(colorCode, dmrEnabled)
	}
	return m.setConfigV1(colorCode, dmrEnabled)
}

func (m *Modem) setConfigV1(colorCode int, dmrEnabled bool) bool {
	b := make([]byte, 26)
	b[0] = 0
	if m.rxInvert {
		b[0] |= 0x01
	}
	if m.txInvert {
		b[0] |= 0x02
	}
	if m.pttInvert {
		b[0] |= 0x04
	}
	if m.debug {
		b[0] |= 0x10
	}
	if !m.duplex {
		b[0] |= 0x80
	}
	if dmrEnabled {
		b[1] = 0x02
	}
	b[2] = byte(m.txDelay / 10)
	b[3] = protocol.ModeIdle
	b[4] = 0xFF
	b[5] = 0xFF
	b[6] = byte(colorCode & 0x0F)
	b[7] = byte(m.dmrDelay)
	b[8] = 128
	b[9] = 0xFF
	b[10] = 0xFF
	b[11] = 0xFF
	b[12] = byte((m.txDC + 128) & 0xFF)
	b[13] = byte((m.rxDC + 128) & 0xFF)
	return m.sendConfig(b)
}

func (m *Modem) setConfigV2(colorCode int, dmrEnabled bool) bool {
	b := make([]byte, 40)
	b[0] = 0
	if m.rxInvert {
		b[0] |= 0x01
	}
	if m.txInvert {
		b[0] |= 0x02
	}
	if m.pttInvert {
		b[0] |= 0x04
	}
	if m.debug {
		b[0] |= 0x10
	}
	if !m.duplex {
		b[0] |= 0x80
	}
	if dmrEnabled {
		b[1] = 0x02
	}
	b[2] = byte(m.txDelay / 10)
	b[3] = protocol.ModeIdle
	b[4] = byte(colorCode & 0x0F)
	b[5] = byte(m.dmrDelay)
	b[6] = 80
	for i := 7; i <= 13; i++ {
		b[i] = 0xFF
	}
	b[14] = byte((m.txDC + 128) & 0xFF)
	b[15] = byte((m.rxDC + 128) & 0xFF)
	b[16] = 0xFF
	b[17] = 0xFF
	return m.sendConfig(b)
}

func (m *Modem) sendConfig(b []byte) bool {
	for attempt := 0; attempt < 3; attempt++ {
		if err := m.send(protocol.MMDVMSetConfig, b); err != nil {
			return false
		}
		f := m.waitResponse(map[byte]bool{protocol.MMDVMAck: true, protocol.MMDVMNak: true}, 3*time.Second)
		if f == nil {
			if attempt < 2 {
				log.Warningf("MMDVM did not respond to SET_CONFIG, retrying (%d/3)", attempt+2)
				continue
			}
			log.Errorf("MMDVM did not respond to SET_CONFIG")
			return false
		}
		if f.Type == protocol.MMDVMNak {
			log.Errorf("SET_CONFIG was NAK'd")
			return false
		}
		log.Infof("MMDVM config written (protocol v%d)", m.ProtocolVersion)
		return true
	}
	return false
}

func (m *Modem) SetMode(mode int) bool {
	for attempt := 0; attempt < 4; attempt++ {
		_ = m.send(protocol.MMDVMSetMode, []byte{byte(mode)})
		f := m.waitResponse(map[byte]bool{protocol.MMDVMAck: true, protocol.MMDVMNak: true}, time.Second)
		if f == nil {
			if attempt < 3 {
				log.Warningf("No ACK for SET_MODE, retrying (%d/4)", attempt+2)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			log.Warningf("No ACK for SET_MODE")
			return false
		}
		if f.Type == protocol.MMDVMNak {
			log.Errorf("SET_MODE was NAK'd (mode %d)", mode)
			return false
		}
		m.mode.Store(int32(mode))
		return true
	}
	return false
}

// SetFrequency sends SET_FREQ (RX/TX Hz little-endian, power byte).
func (m *Modem) SetFrequency(rxHz, txHz, rfLevel int) bool {
	rfPower := 255
	if rfLevel > 0 && rfLevel <= 100 {
		rfPower = int(float64(rfLevel)*2.55 + 0.5)
	}
	payload := make([]byte, 14)
	rx := uint32(rxHz)
	tx := uint32(txHz)
	for i := 0; i < 4; i++ {
		payload[1+i] = byte(rx >> (8 * i))
		payload[5+i] = byte(tx >> (8 * i))
	}
	payload[9] = byte(rfPower)
	_ = m.send(protocol.MMDVMSetFreq, payload)
	for attempt := 0; attempt < 3; attempt++ {
		f := m.waitResponse(map[byte]bool{protocol.MMDVMAck: true, protocol.MMDVMNak: true}, 3*time.Second)
		if f == nil {
			if attempt < 2 {
				log.Warningf("SET_FREQ: no response, retrying (%d/3)", attempt+2)
				_ = m.send(protocol.MMDVMSetFreq, payload)
				continue
			}
			log.Errorf("SET_FREQ failed")
			return false
		}
		if f.Type == protocol.MMDVMNak {
			log.Errorf("SET_FREQ was NAK'd")
			return false
		}
		log.Infof("Frequency set RX=%d TX=%d rf_power=%d", rxHz, txHz, rfPower)
		return true
	}
	return false
}

func (m *Modem) GetStatus() { _ = m.send(protocol.MMDVMGetStatus, nil) }

// WriteDMRData sends a 33-byte slot to the radio (OpenGD77: DMR_DATA2, slot 2).
func (m *Modem) WriteDMRData(slot int, burst []byte) error {
	if len(burst) != protocol.DMRFrameLengthBytes {
		return NewModemError("bad burst length %d", len(burst))
	}
	if slot != 2 {
		return NewModemError("OpenGD77 hotspot only supports slot 2 (simplex)")
	}
	payload := make([]byte, 0, 34)
	payload = append(payload, 0x00)
	payload = append(payload, burst...)
	return m.send(protocol.MMDVMDMRData2, payload)
}

func (m *Modem) WriteDMRStart() { _ = m.send(protocol.MMDVMDMRStart, nil) }
func (m *Modem) WriteDMRAbort() { _ = m.send(protocol.MMDVMDMRAbort, nil) }
