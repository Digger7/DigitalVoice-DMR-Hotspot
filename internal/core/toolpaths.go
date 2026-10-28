package core

import (
	"os"
	"path/filepath"
)

// searchRoots returns absolute directories that may contain helper runtimes
// (python, piper, voices). It looks at the working directory and every
// ancestor of the executable, so tools are still found when the binary is
// launched from somewhere other than the project directory.
func searchRoots() []string {
	seen := make(map[string]bool)
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil || seen[abs] {
			return
		}
		seen[abs] = true
		roots = append(roots, abs)
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		for i := 0; i < 8; i++ {
			add(dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return roots
}
