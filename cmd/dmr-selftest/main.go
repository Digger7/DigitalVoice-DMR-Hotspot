// Command dmr-selftest checks the radio, master reachability and login.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/modem"
	"dmr-hotspot/internal/network"
	"dmr-hotspot/internal/protocol"
)

func stage(name string, ok *bool, detail string) {
	mark := "SKIP"
	if ok != nil {
		if *ok {
			mark = "PASS"
		} else {
			mark = "FAIL"
		}
	}
	if detail != "" {
		fmt.Printf("[%s] %s  -> %s\n", mark, name, detail)
	} else {
		fmt.Printf("[%s] %s\n", mark, name)
	}
}

func main() { os.Exit(run()) }

func run() int {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()
	cfg, err := config.LoadOrInit(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		return 1
	}
	logx.Setup("INFO", 1000)
	for _, e := range cfg.Validate() {
		fmt.Println("  config error:", e)
	}
	stage("config loaded", boolp(true), fmt.Sprintf("callsign=%s id=%d", cfg.Station.Callsign, cfg.Station.DMRID))

	radio := testRadio(cfg)
	master := testMaster(cfg)
	login := false
	if radio && master {
		login = testLogin(cfg)
	}
	fmt.Println()
	switch {
	case radio && master && login:
		fmt.Println("RESULT: everything looks ready - go on air!")
		return 0
	case radio && master:
		fmt.Println("RESULT: radio + master OK. Set DMR ID and hotspot password to test login.")
		return 1
	default:
		return 2
	}
}

func boolp(v bool) *bool { return &v }

func testRadio(cfg config.AppConfig) bool {
	device := cfg.Modem.Port
	if device == "" {
		ports := modem.ListSerialPorts()
		if len(ports) == 0 {
			stage("radio: open", boolp(false), "no serial ports found")
			return false
		}
		device = ports[0]
	}
	port := modem.DetectTransportBaud(device, cfg.Modem.Baud)
	m := modem.NewModem(port, cfg.Modem.Duplex, cfg.Modem.RXInvert, cfg.Modem.TXInvert,
		cfg.Modem.PTTInvert, cfg.Modem.TXDelay, cfg.Modem.DMRDelay,
		cfg.RF.TXDCOffset, cfg.RF.RXDCOffset, false)
	port.SetSink(m)
	if err := port.Open(); err != nil {
		stage("radio: open", boolp(false), err.Error())
		return false
	}
	defer port.Close()
	if !m.GetVersion(3, 1.5) {
		stage("radio: get_version", boolp(false), "no MMDVM response (radio in Hotspot mode?)")
		return false
	}
	stage("radio: version", boolp(true), fmt.Sprintf("proto v%d DMR=%v %q", m.ProtocolVersion, m.HasDMR(), m.Description))
	if !m.SetConfig(cfg.RF.ColorCode, true) {
		stage("radio: set_config", boolp(false), "")
		return false
	}
	stage("radio: set_config", boolp(true), fmt.Sprintf("color code %d", cfg.RF.ColorCode))
	if !m.SetFrequency(cfg.RF.RXFrequency, cfg.RF.TXFrequency, cfg.RF.RFLevel) {
		stage("radio: set_frequency", boolp(false), "")
		return false
	}
	stage("radio: set_frequency", boolp(true), fmt.Sprintf("RX %d TX %d", cfg.RF.RXFrequency, cfg.RF.TXFrequency))
	if !m.SetMode(protocol.ModeDMR) {
		stage("radio: set_mode", boolp(false), "")
		return false
	}
	stage("radio: set_mode", boolp(true), "MODE_DMR")
	return true
}

func testMaster(cfg config.AppConfig) bool {
	candidates := append([]string{cfg.Network.Address}, network.KNOWN_MASTERS...)
	seen := map[string]bool{}
	for _, host := range candidates {
		if seen[host] {
			continue
		}
		seen[host] = true
		addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", host, cfg.Network.Port))
		if err != nil {
			continue
		}
		conn, err := net.DialUDP("udp4", nil, addr)
		if err != nil {
			continue
		}
		id := cfg.Station.DMRID
		if id == 0 {
			id = 2501234
		}
		payload := make([]byte, 8)
		copy(payload, "RPTL")
		binary.BigEndian.PutUint32(payload[4:], uint32(id))
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		_, _ = conn.Write(payload)
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		_ = conn.Close()
		if err == nil && n >= 6 && strings.HasPrefix(string(buf[:n]), "RPTACK") {
			stage("master: reachable", boolp(true), fmt.Sprintf("%s:%d (%s) answered RPTACK", host, cfg.Network.Port, addr.IP))
			return true
		}
		stage("master: reachable", boolp(false), fmt.Sprintf("%s:%d timeout", host, cfg.Network.Port))
	}
	return false
}

func testLogin(cfg config.AppConfig) bool {
	if cfg.Station.DMRID == 0 || cfg.Network.Password == "" {
		stage("login: brandmeister", nil, "set station.dmr_id + network.password to test login")
		return false
	}
	var mu sync.Mutex
	var msgs []string
	logx.Subscribe(func(ts, level, msg string) {
		mu.Lock()
		msgs = append(msgs, msg)
		mu.Unlock()
	})
	cb := network.BuildConfigBytes(cfg.Station.Callsign, cfg.RF.RXFrequency, cfg.RF.TXFrequency,
		cfg.RF.ColorCode, cfg.Network.Slot1, cfg.Network.Slot2, cfg.Modem.Duplex,
		cfg.Network.Name, cfg.RF.RFLevel)
	netw := network.NewBMNetwork(cfg.Network.Address, cfg.Network.Port, cfg.Station.DMRID,
		cfg.Network.Password, cb, cfg.Network.Name)
	fmt.Println("  (logging into BrandMeister ...)")
	netw.Start()
	defer netw.Stop()

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if netw.Status() == "RUNNING" {
			stage("login: brandmeister", boolp(true), fmt.Sprintf("logged in as ID %d", cfg.Station.DMRID))
			return true
		}
		mu.Lock()
		rejected := false
		for _, m := range msgs {
			if strings.Contains(m, "authentication failed") || strings.Contains(m, "MSTNAK") {
				rejected = true
			}
		}
		mu.Unlock()
		if rejected {
			stage("login: brandmeister", boolp(false), "MSTNAK - master rejects DMR ID / hotspot password")
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	stage("login: brandmeister", boolp(false), "no response from master during login")
	return false
}
