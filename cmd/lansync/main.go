// Command lansync runs the LanSync daemon and opens its web UI in the browser.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

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
	guiAddr := flag.String("gui", "127.0.0.1:8384", "address of the web UI (keep it on 127.0.0.1); the next free port is used if busy")
	peerPort := flag.Int("port", 22000, "TCP port other devices connect to; the next free port is used if busy")
	noBrowser := flag.Bool("no-browser", false, "do not open the web UI on start")
	flag.Parse()

	// Starting it twice (e.g. double-clicking again) just opens the running instance.
	urlFile := filepath.Join(*dataDir, "gui-url")
	if b, err := os.ReadFile(urlFile); err == nil && isRunning(string(b)) {
		log.Printf("LanSync is already running at %s", b)
		if !*noBrowser {
			openBrowser(string(b))
		}
		return
	}

	ui, _ := fs.Sub(webFiles, "web")
	node, err := core.New(core.Options{DataDir: *dataDir, APIAddr: *guiAddr, PeerPort: *peerPort, UI: ui, APIFallback: true})
	if err != nil {
		log.Fatal(err)
	}
	if err := node.Start(); err != nil {
		log.Fatal(err)
	}
	os.WriteFile(urlFile, []byte(node.APIURL()), 0o644)
	log.Printf("Open %s in your browser. Keep this window open while you use LanSync.", node.APIURL())
	if !*noBrowser {
		openBrowser(node.APIURL())
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	node.Stop()
}

// isRunning reports whether a LanSync web UI answers at url.
func isRunning(url string) bool {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(url + "/api/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var st struct{ DeviceID string }
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&st) == nil && st.DeviceID != ""
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
