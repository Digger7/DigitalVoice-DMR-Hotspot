// Package network implements the BrandMeister / Homebrew UDP protocol.
package network

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/protocol"
)

var log = logx.GetLogger("net")

// KNOWN_MASTERS is used when the configured master name does not resolve.
var KNOWN_MASTERS = []string{
	"2502.master.brandmeister.network",
	"2503.master.brandmeister.network",
	"2551.master.brandmeister.network",
	"3102.master.brandmeister.network",
	"3103.master.brandmeister.network",
	"2602.master.brandmeister.network",
	"4251.master.brandmeister.network",
}

type DMRData struct {
	SeqNo    int
	SlotNo   int
	SrcID    int
	DstID    int
	FLCO     int
	DataType int
	StreamID int
	BER      int
	RSSI     int
	N        int
	Data     []byte
}

func (d DMRData) IsVoiceSync() bool { return d.DataType == protocol.DTVoiceSync }
func (d DMRData) IsVoice() bool     { return d.DataType == protocol.DTVoice }
func (d DMRData) IsHeader() bool {
	return d.DataType == protocol.DTVoiceLCHeader || d.DataType == protocol.DTDataHeader
}
func (d DMRData) IsTerminator() bool { return d.DataType == protocol.DTTerminatorWithLC }

func PackDMRD(d DMRData, myID int) []byte {
	buf := make([]byte, protocol.HomebrewDataPacketLength)
	copy(buf[0:4], "DMRD")
	buf[4] = byte(d.SeqNo)
	buf[5] = byte((d.SrcID >> 16) & 0xFF)
	buf[6] = byte((d.SrcID >> 8) & 0xFF)
	buf[7] = byte(d.SrcID & 0xFF)
	buf[8] = byte((d.DstID >> 16) & 0xFF)
	buf[9] = byte((d.DstID >> 8) & 0xFF)
	buf[10] = byte(d.DstID & 0xFF)
	binary.BigEndian.PutUint32(buf[11:15], uint32(myID))
	var flags byte
	if d.SlotNo == 2 {
		flags |= 0x80
	}
	if d.FLCO == 1 {
		flags |= 0x40
	}
	switch {
	case d.DataType == protocol.DTVoiceSync:
		flags |= 0x10
	case d.DataType == protocol.DTVoice:
		flags |= byte(d.N & 0x0F)
	default:
		flags |= 0x20 | byte(d.DataType&0x0F)
	}
	buf[15] = flags
	binary.BigEndian.PutUint32(buf[16:20], uint32(d.StreamID))
	copy(buf[20:53], d.Data)
	buf[53] = byte(d.BER & 0xFF)
	buf[54] = byte(d.RSSI & 0xFF)
	return buf
}

func UnpackDMRD(buf []byte) *DMRData {
	if len(buf) < protocol.HomebrewDataPacketLength || !bytes.Equal(buf[0:4], []byte("DMRD")) {
		return nil
	}
	d := &DMRData{}
	d.SeqNo = int(buf[4])
	d.SrcID = int(buf[5])<<16 | int(buf[6])<<8 | int(buf[7])
	d.DstID = int(buf[8])<<16 | int(buf[9])<<8 | int(buf[10])
	if d.SrcID == 0 {
		return nil
	}
	flags := buf[15]
	if flags&0x80 != 0 {
		d.SlotNo = 2
	} else {
		d.SlotNo = 1
	}
	if flags&0x40 != 0 {
		d.FLCO = 1
	}
	hasDataSync := flags&0x20 != 0
	hasVoiceSync := flags&0x10 != 0
	switch {
	case hasDataSync:
		d.DataType = int(flags & 0x0F)
	case hasVoiceSync:
		d.DataType = protocol.DTVoiceSync
	default:
		d.DataType = protocol.DTVoice
		d.N = int(flags & 0x0F)
	}
	d.StreamID = int(binary.BigEndian.Uint32(buf[16:20]))
	d.Data = append([]byte(nil), buf[20:53]...)
	d.BER = int(buf[53])
	d.RSSI = int(buf[54])
	return d
}

// BuildConfigBytes composes the 294-byte text RPTC body used by current
// MMDVMHost / BrandMeister (RPTC + id is prepended by the network layer).
func BuildConfigBytes(callsign string, rxFrequency, txFrequency, colorCode int,
	slot1, slot2, duplex bool, name string, power int) []byte {
	if name == "" {
		name = "DMR Hotspot"
	}
	if power > 99 {
		power = 99
	}
	if power < 0 {
		power = 0
	}
	slots := '4'
	if duplex {
		switch {
		case slot1 && slot2:
			slots = '3'
		case slot1:
			slots = '1'
		case slot2:
			slots = '2'
		}
	}
	return []byte(fmt.Sprintf(
		"%-8.8s%09d%09d%02d%02d%8.8s%9.9s%03d%-20.20s%-19.19s%c%-124.124s%-40.40s%-40.40s",
		callsign, rxFrequency, txFrequency, power, colorCode,
		"0.000000", "00.000000", 0, "", name, slots, "",
		"20260101", "MMDVM_OpenGD77_HS"))
}

// BMNetwork is a BrandMeister Homebrew client.
type BMNetwork struct {
	address      string
	port         int
	dmrID        int
	password     []byte
	config       []byte
	name         string
	pingInterval time.Duration

	mu           sync.Mutex
	conn         *net.UDPConn
	status       string
	waiting      string
	salt         []byte
	lastActivity time.Time
	timeout      time.Duration
	retry        time.Duration
	nextRetry    time.Time
	nextPing     time.Time
	rx           []DMRData
	stop         chan struct{}
	done         chan struct{}
	started      bool
}

func NewBMNetwork(address string, port, dmrID int, password string, configBytes []byte, name string) *BMNetwork {
	if name == "" {
		name = "BM"
	}
	return &BMNetwork{
		address: address, port: port, dmrID: dmrID, password: []byte(password),
		config: configBytes, name: name, pingInterval: 10 * time.Second,
		status: "CLOSED", waiting: "none", timeout: 60 * time.Second,
		retry: 10 * time.Second, rx: make([]DMRData, 0, 256),
	}
}

func (n *BMNetwork) Status() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

func (n *BMNetwork) setStatus(s string) {
	n.mu.Lock()
	n.status = s
	n.mu.Unlock()
}

func (n *BMNetwork) Start() {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return
	}
	n.started = true
	n.stop = make(chan struct{})
	n.done = make(chan struct{})
	stop, done := n.stop, n.done
	n.mu.Unlock()
	go n.run(stop, done)
}

func (n *BMNetwork) Stop() {
	n.mu.Lock()
	if !n.started {
		n.mu.Unlock()
		return
	}
	n.started = false
	stop, done, conn, status := n.stop, n.done, n.conn, n.status
	n.mu.Unlock()

	if conn != nil && status == "RUNNING" {
		payload := make([]byte, 9)
		copy(payload[0:5], "RPTCL")
		binary.BigEndian.PutUint32(payload[5:9], uint32(n.dmrID))
		_, _ = conn.Write(payload)
	}
	close(stop)
	n.closeConn()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	n.setStatus("CLOSED")
}

func (n *BMNetwork) closeConn() {
	n.mu.Lock()
	conn := n.conn
	n.conn = nil
	n.status = "CLOSED"
	n.waiting = "none"
	n.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (n *BMNetwork) GetData(timeout time.Duration) *DMRData {
	deadline := time.Now().Add(timeout)
	for {
		n.mu.Lock()
		if len(n.rx) > 0 {
			d := n.rx[0]
			n.rx = n.rx[1:]
			n.mu.Unlock()
			return &d
		}
		n.mu.Unlock()
		if timeout <= 0 || time.Now().After(deadline) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (n *BMNetwork) GetDataForSlot(slot int, timeout time.Duration) *DMRData {
	deadline := time.Now().Add(timeout)
	for {
		n.mu.Lock()
		for i, d := range n.rx {
			if d.SlotNo == slot {
				n.rx = append(n.rx[:i], n.rx[i+1:]...)
				n.mu.Unlock()
				return &d
			}
		}
		n.mu.Unlock()
		if timeout <= 0 || time.Now().After(deadline) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (n *BMNetwork) push(d DMRData) {
	n.mu.Lock()
	if len(n.rx) < 2000 {
		n.rx = append(n.rx, d)
	}
	n.mu.Unlock()
}

func (n *BMNetwork) SendData(d DMRData) bool {
	if n.Status() != "RUNNING" {
		return false
	}
	if err := n.send(PackDMRD(d, n.dmrID)); err != nil {
		log.Warningf("send DMRD failed: %s", err)
		return false
	}
	return true
}

func (n *BMNetwork) run(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n.connectAndRun(stop)
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (n *BMNetwork) resolve() *net.UDPAddr {
	candidates := append([]string{n.address}, KNOWN_MASTERS...)
	seen := map[string]bool{}
	for _, host := range candidates {
		if seen[host] {
			continue
		}
		seen[host] = true
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			log.Debugf("resolve %s failed: %v", host, err)
			continue
		}
		var ip net.IP
		for _, candidate := range ips {
			if candidate.To4() != nil {
				ip = candidate
				break
			}
		}
		if ip == nil {
			ip = ips[0]
		}
		if host != n.address {
			log.Infof("Using fallback master %s for '%s'", host, n.address)
		}
		return &net.UDPAddr{IP: ip, Port: n.port}
	}
	return nil
}

var lastDNSLog time.Time
var dnsLogMu sync.Mutex

func (n *BMNetwork) connectAndRun(stop <-chan struct{}) {
	n.setStatus("WAITING_LOGIN")
	addr := n.resolve()
	if addr == nil {
		dnsLogMu.Lock()
		now := time.Now()
		if now.Sub(lastDNSLog) > 15*time.Second {
			log.Errorf("Cannot resolve master %s:%d (and no known master resolves)", n.address, n.port)
			lastDNSLog = now
		}
		dnsLogMu.Unlock()
		select {
		case <-stop:
		case <-time.After(2 * time.Second):
		}
		return
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Warningf("network dial failed: %s", err)
		return
	}
	n.mu.Lock()
	n.conn = conn
	n.lastActivity = time.Now()
	n.nextRetry = time.Now()
	n.waiting = "RPTL"
	n.mu.Unlock()

	for {
		select {
		case <-stop:
			n.closeConn()
			return
		default:
		}
		n.mu.Lock()
		now := time.Now()
		waiting := n.waiting
		status := n.status
		nextRetry := n.nextRetry
		nextPing := n.nextPing
		lastActivity := n.lastActivity
		n.mu.Unlock()

		switch {
		case waiting == "RPTL" && !now.Before(nextRetry):
			n.sendRPTL()
			n.mu.Lock()
			n.nextRetry = now.Add(n.retry)
			n.lastActivity = now
			n.mu.Unlock()
		case waiting == "RPTK" && !now.Before(nextRetry):
			n.sendAuthorisation()
			n.mu.Lock()
			n.nextRetry = now.Add(n.retry)
			n.mu.Unlock()
		case waiting == "RPTC" && !now.Before(nextRetry):
			n.sendConfig()
			n.mu.Lock()
			n.nextRetry = now.Add(n.retry)
			n.lastActivity = now
			n.mu.Unlock()
		case status == "RUNNING" && !now.Before(nextPing):
			n.becomeRunning(now)
		}
		if waiting == "MSTCL" {
			return
		}
		if status != "RUNNING" && now.Sub(lastActivity) > n.timeout {
			log.Warningf("network login timed out, reconnecting")
			n.closeConn()
			return
		}
		n.pollRX(stop)
		select {
		case <-stop:
			n.closeConn()
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (n *BMNetwork) becomeRunning(now time.Time) {
	n.mu.Lock()
	if n.status == "RUNNING" {
		if !now.Before(n.nextPing) {
			n.mu.Unlock()
			n.sendRPTPing()
			n.mu.Lock()
			n.nextPing = now.Add(n.pingInterval)
			n.mu.Unlock()
			return
		}
		n.mu.Unlock()
		return
	}
	n.status = "RUNNING"
	n.waiting = "run"
	n.nextPing = now.Add(5 * time.Second)
	n.lastActivity = now
	n.mu.Unlock()
	log.Infof("Logged into %s (%s) as ID %d", n.name, n.address, n.dmrID)
}

func (n *BMNetwork) pollRX(stop <-chan struct{}) {
	n.mu.Lock()
	conn := n.conn
	n.mu.Unlock()
	if conn == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		nr, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if nr >= 4 {
			n.handlePacket(append([]byte(nil), buf[:nr]...))
		}
	}
}

func (n *BMNetwork) handlePacket(data []byte) {
	n.mu.Lock()
	waiting := n.waiting
	n.mu.Unlock()
	if len(data) >= 4 && !bytes.HasPrefix(data, []byte("DMRD")) {
		log.Debugf("net rx %q (%d bytes) while waiting=%s", string(data[:4]), len(data), waiting)
	}

	switch {
	case bytes.HasPrefix(data, []byte("RPTACK")):
		switch waiting {
		case "RPTL":
			n.mu.Lock()
			if len(data) >= 10 {
				n.salt = append([]byte(nil), data[6:10]...)
			} else {
				n.salt = []byte{0, 0, 0, 0}
			}
			n.waiting = "RPTK"
			n.lastActivity = time.Now()
			n.mu.Unlock()
			n.sendAuthorisation()
		case "RPTK":
			n.mu.Lock()
			n.waiting = "RPTC"
			n.nextRetry = time.Now().Add(n.retry)
			n.lastActivity = time.Now()
			n.mu.Unlock()
			n.sendConfig()
		case "RPTC":
			n.mu.Lock()
			n.status = "RUNNING"
			n.waiting = "run"
			n.nextPing = time.Now().Add(5 * time.Second)
			n.lastActivity = time.Now()
			n.mu.Unlock()
			log.Infof("Logged into %s (%s) as ID %d", n.name, n.address, n.dmrID)
		default:
			n.mu.Lock()
			n.lastActivity = time.Now()
			n.mu.Unlock()
		}
	case bytes.HasPrefix(data, []byte("MSTNAK")):
		if waiting == "RPTK" {
			log.Errorf("BrandMeister: authentication failed (incorrect password) for DMR ID %d at RPTK", n.dmrID)
		} else {
			log.Errorf("BrandMeister: the master rejected the config at stage '%s' for DMR ID %d", waiting, n.dmrID)
		}
		n.mu.Lock()
		n.waiting = "RPTL"
		n.nextRetry = time.Now().Add(15 * time.Second)
		n.mu.Unlock()
	case bytes.HasPrefix(data, []byte("MSTCL")):
		log.Warningf("Master is closing the connection")
		n.closeConn()
		n.mu.Lock()
		n.waiting = "MSTCL"
		n.mu.Unlock()
	case bytes.HasPrefix(data, []byte("MSTPONG")):
		n.mu.Lock()
		n.lastActivity = time.Now()
		n.mu.Unlock()
	case bytes.HasPrefix(data, []byte("RPTSBKN")):
		// ignore
	case bytes.HasPrefix(data, []byte("DMRD")):
		if d := UnpackDMRD(data); d != nil {
			n.push(*d)
		}
		n.mu.Lock()
		n.lastActivity = time.Now()
		n.mu.Unlock()
	}
}

func (n *BMNetwork) send(data []byte) error {
	n.mu.Lock()
	conn := n.conn
	n.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("socket closed")
	}
	_, err := conn.Write(data)
	return err
}

func (n *BMNetwork) sendRPTL() {
	log.Debugf("net tx RPTL")
	payload := make([]byte, 8)
	copy(payload[0:4], "RPTL")
	binary.BigEndian.PutUint32(payload[4:8], uint32(n.dmrID))
	_ = n.send(payload)
}

func (n *BMNetwork) sendAuthorisation() {
	log.Debugf("net tx RPTK")
	n.mu.Lock()
	salt := append([]byte(nil), n.salt...)
	n.mu.Unlock()
	if len(salt) < 4 {
		salt = append(salt, make([]byte, 4-len(salt))...)
	}
	h := sha256.New()
	h.Write(salt[:4])
	h.Write(n.password)
	payload := make([]byte, 0, 46)
	payload = append(payload, "RPTK"...)
	id := make([]byte, 4)
	binary.BigEndian.PutUint32(id, uint32(n.dmrID))
	payload = append(payload, id...)
	payload = append(payload, h.Sum(nil)...)
	_ = n.send(payload)
}

func (n *BMNetwork) sendConfig() {
	log.Debugf("net tx RPTC (%d byte config)", len(n.config))
	payload := make([]byte, 0, 8+len(n.config))
	payload = append(payload, "RPTC"...)
	id := make([]byte, 4)
	binary.BigEndian.PutUint32(id, uint32(n.dmrID))
	payload = append(payload, id...)
	payload = append(payload, n.config...)
	_ = n.send(payload)
}

func (n *BMNetwork) sendRPTPing() {
	payload := make([]byte, 11)
	copy(payload[0:7], "RPTPING")
	binary.BigEndian.PutUint32(payload[7:11], uint32(n.dmrID))
	_ = n.send(payload)
}

// Helper for tests / diagnostics.
func (n *BMNetwork) Address() string { return fmt.Sprintf("%s:%d", n.address, n.port) }
