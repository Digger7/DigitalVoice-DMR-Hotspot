// Command dmr-diag performs a serial handshake with an OpenGD77 hotspot.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/modem"
	"dmr-hotspot/internal/protocol"
)

func main() { os.Exit(run()) }

func run() int {
	portFlag := flag.String("port", "", "serial device (default: list ports)")
	baud := flag.Int("baud", 115200, "serial baud")
	cc := flag.Int("cc", 1, "color code")
	freq := flag.Float64("freq", 438.800, "frequency in MHz")
	power := flag.Int("power", 20, "RF level 1..100")
	flag.Parse()

	logx.Setup("DEBUG", 500)
	if *portFlag == "" {
		fmt.Println("ports:", modem.ListSerialPorts())
		return 0
	}
	port := modem.DetectTransportBaud(*portFlag, *baud)
	m := modem.NewModem(port, false, false, false, false, 100, 0, 0, 0, true)
	port.SetSink(m)
	m.SetErrorCallback(func(e string) { fmt.Println("SERIAL ERROR:", e) })
	if err := port.Open(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer port.Close()

	fmt.Println("-- get_version --")
	ok := m.GetVersion(3, 1.5)
	fmt.Printf("version: %v proto %d desc: %q dmr: %v\n", ok, m.ProtocolVersion, m.Description, m.HasDMR())
	if !ok {
		return 1
	}
	hz := int(*freq * 1e6)
	fmt.Println("-- set_config cc --")
	fmt.Println("cfg:", m.SetConfig(*cc, true))
	fmt.Println("-- set_freq --")
	fmt.Println("freq:", m.SetFrequency(hz, hz, *power))
	fmt.Println("-- set_mode DMR --")
	fmt.Println("mode:", m.SetMode(protocol.ModeDMR))
	fmt.Println("-- get_status --")
	m.GetStatus()
	time.Sleep(300 * time.Millisecond)
	if f := m.ReadFrame(300 * time.Millisecond); f != nil {
		fmt.Printf("status: 0x%02X tx=%v cd=%v\n", f.Type, m.TX(), m.CD())
	} else {
		fmt.Println("no status frame")
	}
	fmt.Println("done")
	return 0
}
