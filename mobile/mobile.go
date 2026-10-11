// Package mobile is the gomobile-bindable entry point used by the Android app.
// The app starts the core here and talks to it over the same local REST API as the web UI.
package mobile

import (
	"sync"

	"lansync/core"
)

var (
	mu   sync.Mutex
	node *core.Node
)

// Start runs the LanSync core with its data in dataDir. It returns the local API base URL.
// Calling Start while already running returns the existing URL.
func Start(dataDir, deviceName string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if node != nil {
		return node.APIURL(), nil
	}
	n, err := core.New(core.Options{DataDir: dataDir, DefaultName: deviceName, APIAddr: "127.0.0.1:8384"})
	if err != nil {
		return "", err
	}
	if err := n.Start(); err != nil {
		return "", err
	}
	node = n
	return n.APIURL(), nil
}

// Stop shuts the core down.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if node != nil {
		node.Stop()
		node = nil
	}
}
