package core

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/modem"
	"dmr-hotspot/internal/network"
	"dmr-hotspot/internal/protocol"
)

type RuntimeStatus struct {
	State            string    `json:"state"`
	ModemOK          bool      `json:"modem_ok"`
	ModemDescription string    `json:"modem_description"`
	ModemPort        string    `json:"modem_port"`
	NetStatus        string    `json:"net_status"`
	TX               bool      `json:"tx"`
	RX               bool      `json:"rx"`
	LastError        string    `json:"last_error"`
	StartTime        time.Time `json:"start_time"`
}

type LastHeardEntry struct {
	TS       string  `json:"ts"`
	Slot     int     `json:"slot"`
	Src      int     `json:"src"`
	Dst      int     `json:"dst"`
	FLCO     string  `json:"flco"`
	Duration float64 `json:"duration"`
	BER      int     `json:"ber"`
}

type EventCallback func(event string, payload any)

// Core owns the modem, the network link and the real-time loop.
type Core struct {
	config config.AppConfig
	mode   string

	mu        sync.Mutex
	status    RuntimeStatus
	slots     [2]*Slot
	txQueue   map[int][][]byte
	lastHeard []LastHeardEntry
	callbacks []EventCallback

	lifecycleMu sync.Mutex
	stop        chan struct{}
	done        chan struct{}
	stopOnce    *sync.Once
	running     bool

	modem        *modem.Modem
	transport    modem.Transport
	net          *network.BMNetwork
	echo         *EchoRepeater
	agent        *Agent
	portOverride modem.Transport
	lastStatus   time.Time
}

func NewCore(cfg config.AppConfig) *Core {
	c := &Core{
		config: cfg, mode: strings.ToLower(cfg.Mode),
		txQueue: map[int][][]byte{1: {}, 2: {}},
	}
	if c.mode == "" {
		c.mode = "network"
	}
	if c.mode == "echo" {
		hold := time.Duration(cfg.Agent.HoldTimeout * float64(time.Second))
		c.echo = NewEchoRepeater(cfg.RF.ColorCode, cfg.Agent.MaxQueue, hold)
		c.echo.SetLastHeardCallback(c.onLastHeard)
	}
	if c.mode == "agent" {
		c.agent = NewAgent(cfg.Agent, cfg.RF.ColorCode)
		c.agent.SetTxGain(cfg.RF.TXGainDB)
		c.agent.SetLastHeardCallback(c.onLastHeard)
	}
	c.slots[0] = NewSlot(1, cfg.RF.ColorCode, cfg.Network.Hang)
	c.slots[1] = NewSlot(2, cfg.RF.ColorCode, cfg.Network.Hang)
	return c
}

// SetPortOverride allows tests (or an external transport) to bypass serial
// discovery. Call before Start.
func (c *Core) SetPortOverride(t modem.Transport) { c.portOverride = t }

func (c *Core) AddCallback(cb EventCallback) {
	c.mu.Lock()
	c.callbacks = append(c.callbacks, cb)
	c.mu.Unlock()
}

func (c *Core) fire(event string, payload any) {
	c.mu.Lock()
	cbs := append([]EventCallback(nil), c.callbacks...)
	c.mu.Unlock()
	for _, cb := range cbs {
		func() {
			defer func() { _ = recover() }()
			cb(event, payload)
		}()
	}
}

func (c *Core) Status() RuntimeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Core) setStatus(fn func(*RuntimeStatus)) {
	c.mu.Lock()
	fn(&c.status)
	c.mu.Unlock()
}

func (c *Core) Running() bool {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.running
}

func (c *Core) Mode() string { return c.mode }

func (c *Core) Echo() *EchoRepeater         { return c.echo }
func (c *Core) Agent() *Agent               { return c.agent }
func (c *Core) Network() *network.BMNetwork { return c.net }

func (c *Core) Start() error {
	c.lifecycleMu.Lock()
	if c.running {
		c.lifecycleMu.Unlock()
		log.Warningf("already running")
		return fmt.Errorf("already running")
	}
	c.stop = make(chan struct{})
	c.done = make(chan struct{})
	c.stopOnce = &sync.Once{}
	c.running = true
	stop, done := c.stop, c.done
	c.lifecycleMu.Unlock()

	c.setStatus(func(s *RuntimeStatus) {
		s.State = "STARTING"
		s.LastError = ""
		s.TX, s.RX = false, false
	})
	go c.run(stop, done)
	return nil
}

func (c *Core) Stop() {
	c.lifecycleMu.Lock()
	if !c.running {
		c.lifecycleMu.Unlock()
		return
	}
	stopOnce, done := c.stopOnce, c.done
	c.lifecycleMu.Unlock()

	stopOnce.Do(func() { close(c.stop) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.lifecycleMu.Lock()
	c.running = false
	c.lifecycleMu.Unlock()
}

func (c *Core) run(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer func() {
		c.lifecycleMu.Lock()
		c.running = false
		c.lifecycleMu.Unlock()
	}()

	if err := c.startHardware(); err != nil {
		c.setStatus(func(s *RuntimeStatus) {
			s.State = "ERROR"
			s.LastError = err.Error()
		})
		log.Errorf("startup failed: %s", err)
		c.fire("error", err.Error())
		c.stopHardware()
		return
	}
	if isClosed(stop) {
		c.stopHardware()
		return
	}

	c.setStatus(func(s *RuntimeStatus) {
		s.State = "RUNNING"
		s.StartTime = time.Now()
		switch c.mode {
		case "echo":
			s.NetStatus = "ECHO"
		case "agent":
			s.NetStatus = "AGENT"
		default:
			if c.net != nil {
				s.NetStatus = c.net.Status()
			} else {
				s.NetStatus = "CLOSED"
			}
		}
	})
	c.fire("started", map[string]any{
		"port":        c.Status().ModemPort,
		"description": c.Status().ModemDescription,
	})

	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			c.stopHardware()
			c.markStopped()
			c.fire("stopped", nil)
			return
		case <-ticker.C:
			c.tick()
		}
	}
}

func (c *Core) markStopped() {
	c.setStatus(func(s *RuntimeStatus) {
		if s.State != "ERROR" {
			s.State = "STOPPED"
		}
		s.TX, s.RX = false, false
	})
}

func (c *Core) startHardware() error {
	cfg := c.config
	var port modem.Transport
	if c.portOverride != nil {
		port = c.portOverride
	} else {
		device := cfg.Modem.Port
		if device == "" {
			found := modem.ListSerialPorts()
			if len(found) == 0 {
				return fmt.Errorf("no serial ports found; specify modem.port")
			}
			device = found[0]
			log.Infof("auto-selected serial port %s", device)
		}
		port = modem.DetectTransportBaud(device, cfg.Modem.Baud)
	}
	if err := port.Open(); err != nil {
		return fmt.Errorf("cannot open %s: %w", port.Device(), err)
	}
	c.transport = port
	c.modem = modem.NewModem(port, cfg.Modem.Duplex, cfg.Modem.RXInvert,
		cfg.Modem.TXInvert, cfg.Modem.PTTInvert, cfg.Modem.TXDelay,
		cfg.Modem.DMRDelay, cfg.RF.TXDCOffset, cfg.RF.RXDCOffset, cfg.Modem.Debug)
	port.SetSink(c.modem)

	if !c.modem.GetVersion(3, 1.5) {
		return fmt.Errorf("could not read MMDVM version (check radio is in Hotspot mode)")
	}
	if !c.modem.HasDMR() && c.modem.ProtocolVersion == 2 {
		log.Warningf("modem reports no DMR capability!")
	}
	c.modem.SetErrorCallback(c.onSerialError)
	c.setStatus(func(s *RuntimeStatus) {
		s.ModemPort = port.Device()
		s.ModemDescription = c.modem.Description
		if s.ModemDescription == "" {
			s.ModemDescription = fmt.Sprintf("proto v%d", c.modem.ProtocolVersion)
		}
	})

	if !c.modem.SetConfig(cfg.RF.ColorCode, true) {
		return fmt.Errorf("SET_CONFIG failed")
	}
	if !c.modem.SetFrequency(cfg.RF.RXFrequency, cfg.RF.TXFrequency, cfg.RF.RFLevel) {
		return fmt.Errorf("SET_FREQ failed")
	}
	c.modem.SetMode(protocol.ModeDMR)
	c.setStatus(func(s *RuntimeStatus) { s.ModemOK = true })

	if c.mode == "echo" || c.mode == "agent" {
		if c.mode == "echo" {
			log.Infof("Echo repeater mode")
		} else {
			log.Infof("Agent mode")
		}
		return nil
	}

	configBytes := network.BuildConfigBytes(cfg.Station.Callsign, cfg.RF.RXFrequency,
		cfg.RF.TXFrequency, cfg.RF.ColorCode, cfg.Network.Slot1, cfg.Network.Slot2,
		cfg.Modem.Duplex, cfg.Network.Name, cfg.RF.RFLevel)
	c.net = network.NewBMNetwork(cfg.Network.Address, cfg.Network.Port,
		cfg.Station.DMRID, cfg.Network.Password, configBytes, cfg.Network.Name)
	c.net.Start()
	return nil
}

func (c *Core) stopHardware() {
	if c.net != nil {
		c.net.Stop()
		c.net = nil
	}
	if c.agent != nil {
		c.agent.Close()
	}
	if c.modem != nil {
		c.modem.SetMode(protocol.ModeIdle)
	}
	if c.transport != nil {
		_ = c.transport.Close()
		c.transport = nil
	}
}

func (c *Core) onSerialError(msg string) {
	log.Errorf("Serial link lost: %s", msg)
	c.setStatus(func(s *RuntimeStatus) {
		s.LastError = msg
		s.State = "ERROR"
	})
	c.fire("error", msg)
	c.lifecycleMu.Lock()
	stopOnce := c.stopOnce
	c.lifecycleMu.Unlock()
	if stopOnce != nil {
		stopOnce.Do(func() { close(c.stop) })
	}
}

func (c *Core) tick() {
	m := c.modem
	if m == nil {
		return
	}
	now := time.Now()
	if now.Sub(c.lastStatus) > 500*time.Millisecond {
		c.lastStatus = now
		m.GetStatus()
	}

	for i := 0; i < 8; i++ {
		frame := m.ReadFrame(0)
		if frame == nil {
			break
		}
		c.handleFrame(frame)
	}

	switch c.mode {
	case "echo":
		c.echoTick(m)
	case "agent":
		c.agentTick(m)
	default:
		if c.net == nil {
			return
		}
		if c.config.Network.Slot2 {
			c.drainNetSlot(2)
		}
	}

	netChanged := ""
	c.setStatus(func(s *RuntimeStatus) {
		s.TX = m.TX()
		s.RX = m.CD()
		if c.net != nil {
			st := c.net.Status()
			if st != s.NetStatus {
				s.NetStatus = st
				netChanged = st
			}
		}
	})
	if netChanged != "" {
		c.fire("netstatus", netChanged)
	}
}

func (c *Core) echoTick(m *modem.Modem) {
	if c.echo == nil {
		return
	}
	space := m.DMRSpace2()
	if !m.HasStatus() {
		space = 10
	}
	for _, burst := range c.echo.Flush(space, time.Now()) {
		if err := m.WriteDMRData(2, burst); err != nil {
			break
		}
		m.NoteDMR2Sent(1)
	}
}

func (c *Core) agentTick(m *modem.Modem) {
	if c.agent == nil {
		return
	}
	c.agent.Tick(time.Now())
	space := m.DMRSpace2()
	if !m.HasStatus() {
		space = 10
	}
	for _, burst := range c.agent.Flush(space, time.Now()) {
		if err := m.WriteDMRData(2, burst); err != nil {
			break
		}
		m.NoteDMR2Sent(1)
	}
}

func (c *Core) handleFrame(frame *modem.Frame) {
	switch frame.Type {
	case protocol.MMDVMDMRData1, protocol.MMDVMDMRData2:
		switch c.mode {
		case "echo":
			c.rfEcho(frame.Data)
		case "agent":
			c.rfAgent(frame.Data)
		default:
			if frame.Type == protocol.MMDVMDMRData2 {
				c.rfRX(frame.Data)
			}
		}
	case protocol.MMDVMDMRLost1, protocol.MMDVMDMRLost2:
		if c.echo != nil {
			c.echo.HandleLost()
		}
		if c.agent != nil {
			c.agent.HandleLost()
		}
		log.Debugf("RF loss on the modem")
	}
}

func splitRF(payload []byte) (byte, []byte, int, bool) {
	if len(payload) < 34 {
		return 0, nil, 0, false
	}
	meta := payload[0]
	slot := payload[1:34]
	rssi := 0
	if len(payload) >= 36 {
		rssi = int(payload[34])<<8 | int(payload[35])
	}
	return meta, slot, rssi, true
}

func (c *Core) rfEcho(payload []byte) {
	meta, slot, rssi, ok := splitRF(payload)
	if !ok || c.echo == nil {
		return
	}
	c.echo.Ingest(meta, slot, rssi)
}

func (c *Core) rfAgent(payload []byte) {
	meta, slot, rssi, ok := splitRF(payload)
	if !ok || c.agent == nil {
		return
	}
	c.agent.Ingest(meta, slot, rssi)
}

func (c *Core) rfRX(payload []byte) {
	meta, burst, rssi, ok := splitRF(payload)
	if !ok || len(burst) != protocol.DMRFrameLengthBytes {
		return
	}
	const slotNo = 2
	d := c.slots[slotNo-1].ProcessRFFrame(meta, burst, rssi)
	if d == nil {
		return
	}
	if c.net != nil && c.net.SendData(*d) {
		if d.DataType == protocol.DTVoiceLCHeader {
			log.Infof("RF voice %d -> %s%d (slot %d)", d.SrcID,
				flcoLabel(d.FLCO), d.DstID, slotNo)
		}
		c.updateLastHeard(slotNo, *d)
	}
}

func (c *Core) drainNetSlot(slotNo int) {
	if c.net == nil || slotNo != 2 || c.modem == nil {
		return
	}
	d := c.net.GetDataForSlot(slotNo, 0)
	if d == nil || len(d.Data) != protocol.DMRFrameLengthBytes {
		return
	}
	if d.DataType == protocol.DTVoiceLCHeader {
		log.Infof("net voice %d -> %s%d (slot %d)", d.SrcID,
			flcoLabel(d.FLCO), d.DstID, slotNo)
	}
	_ = c.modem.WriteDMRData(slotNo, d.Data)
}

func flcoLabel(flco int) string {
	if flco == 0 {
		return "TG "
	}
	return "PC "
}

func (c *Core) onLastHeard(src, dst int, flco string) {
	c.mu.Lock()
	entry := LastHeardEntry{TS: time.Now().Format("15:04:05"), Slot: 2,
		Src: src, Dst: dst, FLCO: flco}
	c.lastHeard = append([]LastHeardEntry{entry}, c.lastHeard...)
	if len(c.lastHeard) > 40 {
		c.lastHeard = c.lastHeard[:40]
	}
	c.mu.Unlock()
	c.fire("lastheard", entry)
}

func (c *Core) updateLastHeard(slotNo int, d network.DMRData) {
	c.mu.Lock()
	entry := LastHeardEntry{TS: time.Now().Format("15:04:05"), Slot: slotNo,
		Src: d.SrcID, Dst: d.DstID, FLCO: strings.TrimSpace(flcoLabel(d.FLCO)), BER: d.BER}
	c.lastHeard = append([]LastHeardEntry{entry}, c.lastHeard...)
	if len(c.lastHeard) > 40 {
		c.lastHeard = c.lastHeard[:40]
	}
	c.mu.Unlock()
	c.fire("lastheard", entry)
}

func (c *Core) LastHeard() []LastHeardEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LastHeardEntry(nil), c.lastHeard...)
}

func (c *Core) Config() config.AppConfig { return c.config }

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
