package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Mode controls which direction a folder syncs in.
type Mode string

const (
	SendReceive Mode = "sendreceive"
	SendOnly    Mode = "sendonly"
	ReceiveOnly Mode = "receiveonly"
)

func (m Mode) valid() bool { return m == SendReceive || m == SendOnly || m == ReceiveOnly }

// Device is a known (added) remote device.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Addr is an optional manual "host:port" used when discovery cannot see the device.
	Addr string `json:"addr,omitempty"`
}

// Folder is a synced folder. The same folder ID is used on every device that shares it.
type Folder struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Path    string   `json:"path"`
	Mode    Mode     `json:"mode"`
	Devices []string `json:"devices"`
}

func (f Folder) sharedWith(id string) bool {
	for _, d := range f.Devices {
		if d == id {
			return true
		}
	}
	return false
}

// Config is persisted as config.json in the data directory.
type Config struct {
	DeviceID   string   `json:"deviceID"`
	DeviceName string   `json:"deviceName"`
	Devices    []Device `json:"devices"`
	Folders    []Folder `json:"folders"`
}

func loadConfig(path, defaultName string) (Config, error) {
	var c Config
	err := readJSON(path, &c)
	if errors.Is(err, os.ErrNotExist) {
		c.DeviceID = newID(16)
		c.DeviceName = defaultName
		return c, writeJSON(path, c)
	}
	return c, err
}

// writeJSON writes v atomically (temp file + rename) so a crash never leaves a half-written file.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// newID returns a random upper-case hex ID with n random bytes, grouped for readability.
func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	s := strings.ToUpper(hex.EncodeToString(b))
	var parts []string
	for len(s) > 8 {
		parts = append(parts, s[:8])
		s = s[8:]
	}
	return strings.Join(append(parts, s), "-")
}
