// Command dmr-gui serves the local web UI and opens it in the browser.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/webui"
)

func main() { os.Exit(run()) }

func run() int {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	addr := flag.String("addr", "127.0.0.1:8080", "web UI listen address")
	noStart := flag.Bool("no-start", false, "do not start the hotspot automatically")
	noBrowser := flag.Bool("no-browser", false, "do not open the browser")
	flag.Parse()

	cfg, err := config.LoadOrInit(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		return 1
	}
	logx.Setup(cfg.LogLevel, 2000)
	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			logx.GetLogger("config").Warningf("%s", e)
		}
	}

	srv := webui.New(cfg, *cfgPath)
	if !*noStart {
		if err := srv.Start(); err != nil {
			logx.GetLogger("gui").Warningf("not started: %s", err)
		}
	}
	url := "http://" + *addr
	if !*noBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(url)
		}()
	}
	if err := srv.ListenAndServe(*addr); err != nil {
		fmt.Fprintln(os.Stderr, "web server:", err)
		return 1
	}
	return 0
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
