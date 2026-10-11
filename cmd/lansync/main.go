// Command lansync runs the LanSync daemon and opens its web UI in the browser.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"lansync/core"
)

//go:embed web
var webFiles embed.FS

func main() {
	defaultData := "lansync-data"
	if dir, err := os.UserConfigDir(); err == nil {
		defaultData = filepath.Join(dir, "lansync")
	}
	dataDir := flag.String("data", defaultData, "directory for config and indexes")
	guiAddr := flag.String("gui", "127.0.0.1:8384", "address of the web UI (keep it on 127.0.0.1)")
	peerPort := flag.Int("port", 22000, "TCP port other devices connect to")
	noBrowser := flag.Bool("no-browser", false, "do not open the web UI on start")
	flag.Parse()

	ui, _ := fs.Sub(webFiles, "web")
	node, err := core.New(core.Options{DataDir: *dataDir, APIAddr: *guiAddr, PeerPort: *peerPort, UI: ui})
	if err != nil {
		log.Fatal(err)
	}
	if err := node.Start(); err != nil {
		log.Fatal(err)
	}
	if !*noBrowser {
		openBrowser(node.APIURL())
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	node.Stop()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("open %s in your browser", url)
	}
}
