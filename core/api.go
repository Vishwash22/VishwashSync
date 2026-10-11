package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Local REST API for the UIs. It listens on loopback only.

type deviceView struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Addr     string    `json:"addr,omitempty"`     // manual address
	SeenAddr string    `json:"seenAddr,omitempty"` // address learned from the network
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"lastSeen,omitempty"`
}

type folderView struct {
	Folder
	Status FolderStatus `json:"status"`
}

type offerView struct {
	DeviceID   string `json:"deviceID"`
	DeviceName string `json:"deviceName"`
	FolderID   string `json:"folderID"`
	Label      string `json:"label"`
}

func (n *Node) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		defer n.mu.Unlock()
		writeJSONResponse(w, map[string]any{
			"deviceID": n.cfg.DeviceID, "deviceName": n.cfg.DeviceName, "nameSet": n.cfg.NameSet,
			"peerPort": n.opts.PeerPort, "addresses": localAddrs(n.opts.PeerPort),
		})
	})
	mux.HandleFunc("PUT /api/status", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ DeviceName string }
		if !decode(w, r, &req) {
			return
		}
		name := strings.TrimSpace(req.DeviceName)
		if name == "" || utf8.RuneCountInString(name) > 64 {
			httpError(w, errors.New("device name must be 1 to 64 characters"))
			return
		}
		n.mu.Lock()
		n.cfg.DeviceName, n.cfg.NameSet = name, true
		err := n.saveConfigLocked()
		n.mu.Unlock()
		n.Announce() // let other devices pick up the new name right away
		reply(w, err)
	})
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		n.probeManualDevices()
		known, discovered := []deviceView{}, []deviceView{}
		n.mu.Lock()
		isKnown := map[string]bool{}
		for _, d := range n.cfg.Devices {
			isKnown[d.ID] = true
			v := deviceView{ID: d.ID, Name: d.Name, Addr: d.Addr}
			if s, ok := n.seen[d.ID]; ok {
				v.SeenAddr, v.LastSeen, v.Online = s.Addr, s.LastSeen, time.Since(s.LastSeen) < 3*time.Minute
			}
			known = append(known, v)
		}
		for id, s := range n.seen {
			if !isKnown[id] && time.Since(s.LastSeen) < 3*time.Minute {
				discovered = append(discovered, deviceView{ID: id, Name: s.Name, SeenAddr: s.Addr, Online: true, LastSeen: s.LastSeen})
			}
		}
		n.mu.Unlock()
		sort.Slice(discovered, func(i, j int) bool { return discovered[i].Name < discovered[j].Name })
		writeJSONResponse(w, map[string]any{"known": known, "discovered": discovered})
	})
	mux.HandleFunc("POST /api/devices", func(w http.ResponseWriter, r *http.Request) {
		var d Device
		if !decode(w, r, &d) {
			return
		}
		d.ID, d.Name, d.Addr = strings.ToUpper(strings.TrimSpace(d.ID)), strings.TrimSpace(d.Name), strings.TrimSpace(d.Addr)
		if d.Addr != "" {
			if _, _, err := net.SplitHostPort(d.Addr); err != nil {
				d.Addr = net.JoinHostPort(d.Addr, "22000") // just an IP: use the default port
			}
		}
		if d.ID == "" {
			// Added by address only: ask the device who it is. Our request also tells it about us,
			// so this device shows up on the other one right away.
			if d.Addr == "" {
				httpError(w, errors.New("enter the other device's address (or its device ID)"))
				return
			}
			var a announcement
			if err := n.peerGetJSON(d.Addr, "/peer/hello", &a); err != nil || a.Magic != discoveryMagic {
				httpError(w, fmt.Errorf("couldn't reach LanSync at %s. Check the address and that LanSync is running there", d.Addr))
				return
			}
			d.ID = a.ID
			if d.Name == "" {
				d.Name = a.Name
			}
			n.markSeen(a.ID, a.Name, d.Addr)
		}
		n.mu.Lock()
		if d.ID == n.cfg.DeviceID {
			n.mu.Unlock()
			httpError(w, errors.New("that is this device's own ID"))
			return
		}
		if d.Name == "" {
			d.Name = n.seen[d.ID].Name
		}
		replaced := false
		for i := range n.cfg.Devices {
			if n.cfg.Devices[i].ID == d.ID {
				n.cfg.Devices[i], replaced = d, true
			}
		}
		if !replaced {
			n.cfg.Devices = append(n.cfg.Devices, d)
		}
		err := n.saveConfigLocked()
		n.mu.Unlock()
		reply(w, err)
	})
	mux.HandleFunc("DELETE /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		n.mu.Lock()
		devs := n.cfg.Devices[:0]
		for _, d := range n.cfg.Devices {
			if d.ID != id {
				devs = append(devs, d)
			}
		}
		n.cfg.Devices = devs
		for i, f := range n.cfg.Folders {
			n.cfg.Folders[i].Devices = without(f.Devices, id)
		}
		err := n.saveConfigLocked()
		n.mu.Unlock()
		reply(w, err)
	})
	mux.HandleFunc("POST /api/discover", func(w http.ResponseWriter, r *http.Request) {
		n.Announce()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/folders", func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		out := []folderView{}
		for _, f := range n.cfg.Folders {
			out = append(out, folderView{Folder: f, Status: *n.folderStatus(f.ID)})
		}
		n.mu.Unlock()
		writeJSONResponse(w, out)
	})
	mux.HandleFunc("POST /api/folders", func(w http.ResponseWriter, r *http.Request) {
		var f Folder
		if !decode(w, r, &f) {
			return
		}
		f, err := n.saveFolder(f)
		if err != nil {
			httpError(w, err)
			return
		}
		writeJSONResponse(w, f)
	})
	mux.HandleFunc("DELETE /api/folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		n.mu.Lock()
		kept := n.cfg.Folders[:0]
		for _, f := range n.cfg.Folders {
			if f.ID != id {
				kept = append(kept, f)
			}
		}
		n.cfg.Folders = kept
		delete(n.indexes, id)
		delete(n.status, id)
		err := n.saveConfigLocked()
		n.mu.Unlock()
		os.Remove(n.indexPath(id)) // the synced files themselves are left alone
		reply(w, err)
	})
	mux.HandleFunc("POST /api/folders/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := n.folder(id); !ok {
			http.NotFound(w, r)
			return
		}
		// Show the folder as busy right away instead of "Up to date" until the sync starts.
		n.updateStatus(id, func(s *FolderStatus) {
			if s.State == "idle" || s.State == "error" {
				s.State, s.LastError = "scanning", ""
			}
		})
		go n.SyncFolder(id, true)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /api/offers", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, n.offers())
	})
	mux.HandleFunc("GET /api/browse", n.handleBrowse)
	mux.HandleFunc("GET /api/log", func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		lines := append([]string{}, n.logLines...)
		n.mu.Unlock()
		writeJSONResponse(w, lines)
	})
	if n.opts.UI != nil {
		mux.Handle("/", http.FileServerFS(n.opts.UI))
	}
	return mux
}

// saveFolder validates and adds or updates a folder. A missing directory is created.
func (n *Node) saveFolder(f Folder) (Folder, error) {
	f.ID, f.Label, f.Path = strings.TrimSpace(f.ID), strings.TrimSpace(f.Label), strings.TrimSpace(f.Path)
	if f.ID == "" {
		f.ID = newID(5)
	}
	if f.Mode == "" {
		f.Mode = SendReceive
	}
	if !f.Mode.valid() {
		return f, errors.New("mode must be sendreceive, sendonly or receiveonly")
	}
	if !filepath.IsAbs(f.Path) {
		return f, errors.New("folder path must be absolute")
	}
	if err := os.MkdirAll(f.Path, 0o755); err != nil {
		return f, err
	}
	if f.Label == "" {
		f.Label = filepath.Base(f.Path)
	}
	if f.Devices == nil {
		f.Devices = []string{}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	known := map[string]bool{}
	for _, d := range n.cfg.Devices {
		known[d.ID] = true
	}
	for _, d := range f.Devices {
		if !known[d] {
			return f, errors.New("unknown device " + d + "; add it first")
		}
	}
	replaced := false
	for i := range n.cfg.Folders {
		if n.cfg.Folders[i].ID == f.ID {
			if n.cfg.Folders[i].Path != f.Path {
				delete(n.indexes, f.ID) // different directory: start a fresh index
				os.Remove(n.indexPath(f.ID))
			}
			n.cfg.Folders[i], replaced = f, true
		}
	}
	if !replaced {
		n.cfg.Folders = append(n.cfg.Folders, f)
	}
	return f, n.saveConfigLocked()
}

// offers asks known devices which folders they share with us that we haven't added yet.
func (n *Node) offers() []offerView {
	n.mu.Lock()
	have := map[string]bool{}
	for _, f := range n.cfg.Folders {
		have[f.ID] = true
	}
	devs := append([]Device{}, n.cfg.Devices...)
	n.mu.Unlock()
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := []offerView{}
	for _, d := range devs {
		addr := n.addrOf(d.ID)
		if addr == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var list []peerFolder
			if n.peerGetJSON(addr, "/peer/folders", &list) != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, pf := range list {
				if !have[pf.ID] {
					out = append(out, offerView{d.ID, d.Name, pf.ID, pf.Label})
				}
			}
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// probeManualDevices pings devices that have a manual address, so they show as online.
func (n *Node) probeManualDevices() {
	n.mu.Lock()
	devs := append([]Device{}, n.cfg.Devices...)
	n.mu.Unlock()
	client := &http.Client{Timeout: time.Second}
	var wg sync.WaitGroup
	for _, d := range devs {
		if d.Addr == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get("http://" + d.Addr + "/peer/hello")
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var a announcement
			if json.NewDecoder(resp.Body).Decode(&a) == nil && a.ID == d.ID {
				n.markSeen(d.ID, a.Name, d.Addr)
			}
		}()
	}
	wg.Wait()
}

func without(list []string, s string) []string {
	out := []string{}
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpError(w, err)
		return false
	}
	return true
}

func httpError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func reply(w http.ResponseWriter, err error) {
	if err != nil {
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
