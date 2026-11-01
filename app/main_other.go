//go:build !windows

// Non-Windows build, used only to test the server: serves the page and opens the default browser.
package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
)

func main() {
	dataDir = appDataDir()
	if d := os.Getenv("KOTEL_DATA_DIR"); d != "" {
		dataDir = d
	}
	os.MkdirAll(dataDir, 0o755)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return
	}
	go http.Serve(ln, newMux(nil, func() { os.Exit(0) }))
	if os.Getenv("KOTEL_NO_BROWSER") == "" {
		exec.Command("xdg-open", "http://"+listenAddr+"/").Start()
	}
	watchBrowserTab()
}

func runInstaller(path string) error {
	return errors.New("обновление работает только в Windows")
}
