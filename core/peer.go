package core

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Peer protocol: plain HTTP on the LAN. v1 has no authentication or encryption; the
// X-LanSync-Device header only scopes which folders a device can see.
const (
	hdrDevice = "X-LanSync-Device"
	hdrName   = "X-LanSync-Name"
	hdrPort   = "X-LanSync-Port"
)

type peerFolder struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (n *Node) peerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /peer/hello", func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		resp := announcement{Magic: discoveryMagic, ID: n.cfg.DeviceID, Name: n.cfg.DeviceName, Port: n.opts.PeerPort}
		n.mu.Unlock()
		writeJSONResponse(w, resp)
	})
	mux.HandleFunc("GET /peer/folders", func(w http.ResponseWriter, r *http.Request) {
		caller := r.Header.Get(hdrDevice)
		out := []peerFolder{}
		n.mu.Lock()
		for _, f := range n.cfg.Folders {
			if f.sharedWith(caller) {
				out = append(out, peerFolder{f.ID, f.Label})
			}
		}
		n.mu.Unlock()
		writeJSONResponse(w, out)
	})
	mux.HandleFunc("GET /peer/folders/{id}/index", func(w http.ResponseWriter, r *http.Request) {
		f, ok := n.sharedFolder(w, r)
		if !ok {
			return
		}
		// Rescan first so the caller sees current files. Skip if this folder is busy syncing
		// (it may be waiting on the caller) and serve the last index instead.
		if l := n.folderLock(f.ID); l.TryLock() {
			n.rescan(f)
			l.Unlock()
		}
		out := []FileInfo{}
		for _, fi := range n.loadIndex(f.ID) {
			if n.offered(f, fi) {
				out = append(out, fi)
			}
		}
		writeJSONResponse(w, out)
	})
	mux.HandleFunc("GET /peer/folders/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		f, ok := n.sharedFolder(w, r)
		if !ok {
			return
		}
		rel := r.URL.Query().Get("path")
		fi, ok := n.loadIndex(f.ID)[rel]
		if !ok || !validRel(rel) || !n.offered(f, fi) {
			http.NotFound(w, r)
			return
		}
		file, err := os.Open(filepath.Join(f.Path, filepath.FromSlash(rel)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", fileModTime(file), file)
	})
	mux.HandleFunc("POST /peer/folders/{id}/sync-request", func(w http.ResponseWriter, r *http.Request) {
		f, ok := n.sharedFolder(w, r)
		if !ok {
			return
		}
		if f.Mode != SendOnly {
			go n.SyncFolder(f.ID, false)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return n.rememberCaller(mux)
}

// offered reports whether a file is offered to peers. Local changes in receive-only folders are not.
func (n *Node) offered(f Folder, fi FileInfo) bool {
	return !(f.Mode == ReceiveOnly && fi.LocalChange)
}

func (n *Node) sharedFolder(w http.ResponseWriter, r *http.Request) (Folder, bool) {
	f, ok := n.folder(r.PathValue("id"))
	if !ok || !f.sharedWith(r.Header.Get(hdrDevice)) {
		http.NotFound(w, r)
		return Folder{}, false
	}
	return f, true
}

// rememberCaller records the caller's address so we can reach it even if its broadcasts don't reach us.
func (n *Node) rememberCaller(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, port := r.Header.Get(hdrDevice), r.Header.Get(hdrPort)
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && id != "" && port != "" {
			if _, err := strconv.Atoi(port); err == nil {
				n.markSeen(id, r.Header.Get(hdrName), net.JoinHostPort(host, port))
			}
		}
		h.ServeHTTP(w, r)
	})
}

// peerRequest builds a request to a peer's API with our identity headers.
func (n *Node) peerRequest(method, addr, path string, query url.Values) (*http.Request, error) {
	u := url.URL{Scheme: "http", Host: addr, Path: path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(n.ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	req.Header.Set(hdrDevice, n.cfg.DeviceID)
	req.Header.Set(hdrName, n.cfg.DeviceName)
	n.mu.Unlock()
	req.Header.Set(hdrPort, strconv.Itoa(n.opts.PeerPort))
	return req, nil
}

func (n *Node) peerGetJSON(addr, path string, v any) error {
	req, err := n.peerRequest("GET", addr, path, nil)
	if err != nil {
		return err
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func fileModTime(f *os.File) time.Time {
	if st, err := f.Stat(); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func writeJSONResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
