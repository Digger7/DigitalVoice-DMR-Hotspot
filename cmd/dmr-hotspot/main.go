// Command dmr-hotspot runs the hotspot headlessly (optionally with the web UI).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/core"
	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/webui"
)

func main() { os.Exit(run()) }

func run() int {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	check := flag.Bool("check", false, "validate config and exit")
	debug := flag.Bool("debug", false, "enable debug logging")
	webAddr := flag.String("web", "", "also serve the web UI on this address, e.g. 127.0.0.1:8080")
	flag.Parse()

	cfg, err := config.LoadOrInit(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %s\n", err)
		return 1
	}
	if *debug {
		cfg.LogLevel = "DEBUG"
	}
	logx.Setup(cfg.LogLevel, 2000)

	errs := cfg.Validate()
	if *check {
		if len(errs) > 0 {
			fmt.Println("configuration errors:")
			for _, e := range errs {
				fmt.Println("  - " + e)
			}
			return 1
		}
		printSummary(cfg)
		return 0
	}
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "config error:", e)
	}
	if len(errs) > 0 {
		return 1
	}

	if *webAddr != "" {
		srv := webui.New(cfg, *cfgPath)
		if err := srv.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "start failed:", err)
			return 1
		}
		if err := srv.ListenAndServe(*webAddr); err != nil {
			fmt.Fprintln(os.Stderr, "web server:", err)
			return 1
		}
		return 0
	}

	c := core.NewCore(cfg)
	c.AddCallback(func(event string, payload any) {
		switch event {
		case "started":
			fmt.Printf("[hotspot] started on %v\n", payload)
		case "netstatus":
			fmt.Printf("[network] %v\n", payload)
		case "lastheard":
			if e, ok := payload.(core.LastHeardEntry); ok {
				fmt.Printf("[lastheard] TS%d %d -> %s %d\n", e.Slot, e.Src, e.FLCO, e.Dst)
			}
		}
	})
	if err := c.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start failed:", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Stop()
			return 0
		case <-ticker.C:
			st := c.Status()
			if st.State == "ERROR" {
				fmt.Fprintln(os.Stderr, "[fatal]", st.LastError)
				c.Stop()
				return 1
			}
		}
	}
}

func printSummary(cfg config.AppConfig) {
	fmt.Println("configuration OK")
	fmt.Printf("  serial : %s @ %d\n", orAuto(cfg.Modem.Port), cfg.Modem.Baud)
	fmt.Printf("  freq   : RX %d  TX %d  CC %d  pwr %d\n", cfg.RF.RXFrequency, cfg.RF.TXFrequency, cfg.RF.ColorCode, cfg.RF.RFLevel)
	fmt.Printf("  net    : %s %d -> %s:%d\n", cfg.Station.Callsign, cfg.Station.DMRID, cfg.Network.Address, cfg.Network.Port)
	fmt.Printf("  slots  : TS1=%v TS2=%v\n", cfg.Network.Slot1, cfg.Network.Slot2)
	if cfg.Mode == "agent" {
		a := cfg.Agent
		llmURL, llmModel := a.ResolveLLM()
		fmt.Printf("  agent  : whisper=%s llm=%s (%s) piper voice=%s\n", a.WhisperModel, orOff(llmURL), orAuto(llmModel), orAuto(a.PiperVoice+" "+a.PiperModelPath))
		if a.WebSearch {
			fmt.Printf("  web    : %s, top %d results, read %d pages\n", a.WebSearchProvider, a.WebSearchMaxResults, a.WebSearchFetchPages)
		}
	}
}

func orAuto(s string) string {
	if s == "" || s == " " {
		return "(auto)"
	}
	return s
}

func orOff(s string) string {
	if s == "" {
		return "(off)"
	}
	return s
}
