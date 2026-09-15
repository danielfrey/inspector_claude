package main

import (
	"os/exec"
	"runtime"
)

// openInBrowser hands a file path (or URL) to the OS default handler. It starts
// the opener without waiting, so the TUI is never blocked.
func openInBrowser(target string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{target}
	case "windows":
		name, args = "cmd", []string{"/c", "start", "", target}
	default: // linux, *bsd
		name, args = "xdg-open", []string{target}
	}
	return exec.Command(name, args...).Start()
}
