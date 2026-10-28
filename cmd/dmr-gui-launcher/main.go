// Command dmr-gui-launcher is the CFBundleExecutable of the macOS app bundle:
// it switches into Contents/Resources and execs dmr-gui with a writable config.
package main

import (
	"os"
	"path/filepath"
	"syscall"

	"dmr-hotspot/internal/config"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	resources := filepath.Join(filepath.Dir(exe), "..", "Resources")
	bin := filepath.Join(resources, "dmr-gui")
	cfg := filepath.Join(resources, "config.yaml")
	if _, err := os.Stat(cfg); err != nil {
		cfg = config.DefaultConfigFile()
	}
	_ = syscall.Chdir(resources)
	args := append([]string{"dmr-gui", "-config", cfg}, os.Args[1:]...)
	syscall.Exec(bin, args, os.Environ())
}
