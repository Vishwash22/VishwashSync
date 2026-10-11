// Package core is the LanSync engine: config, folder scanning, LAN discovery, the peer protocol,
// the sync engine and the local REST API used by the web and Android UIs.
package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Options configure a Node. Zero values get defaults.
type Options struct {
	DataDir       string // where config.json and indexes live (required)
	DefaultName   string // device name used on first run
	APIAddr       string // local UI/REST API, default "127.0.0.1:8384"
	PeerPort      int    // LAN peer API port, default 22000
	DiscoveryPort int    // UDP discovery port, default 21027; -1 disables discovery
	UI            fs.FS  // optional static web UI served at "/"
	// APIFallback lets the local API move to the next free port if APIAddr is busy. The Android app
	// leaves it off because it connects to a fixed address.
	APIFallback bool
}

// Node is one running LanSync instance.
type Node struct {
	opts Options

	mu         sync.Mutex
	cfg        Config
	seen       map[string]seenDevice          // devices heard from via discovery or incoming requests
	indexes    map[string]map[string]FileInfo // folder ID -> index (cache of index/<id>.json)
	status     map[string]*FolderStatus
	locks      map[string]*sync.Mutex // one sync at a time per folder
	logLines   []string
	ctx        context.Context
	cancel     context.CancelFunc
	servers    []*http.Server
	udp        *net.UDPConn
	apiURL     string
	httpClient *http.Client
}

type seenDevice struct {
	Name     string
	Addr     string // host:port of the peer API
	LastSeen time.Time
}

// FolderStatus is the live sync state of a folder shown in the UI.
type FolderStatus struct {
	State     string    `json:"state"` // idle, scanning, syncing, error
	LastSync  time.Time `json:"lastSync"`
	LastError string    `json:"lastError,omitempty"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	// Waiting names the device we're waiting on to finish receiving; Done/Total are then its progress.
	Waiting string `json:"waiting,omitempty"`
}

func New(opts Options) (*Node, error) {
	if opts.DataDir == "" {
		return nil, errors.New("DataDir is required")
	}
	if opts.APIAddr == "" {
		opts.APIAddr = "127.0.0.1:8384"
	}
	if opts.PeerPort == 0 {
		opts.PeerPort = 22000
	}
	if opts.DiscoveryPort == 0 {
		opts.DiscoveryPort = 21027
	}
	if opts.DefaultName == "" {
		opts.DefaultName, _ = os.Hostname()
	}
	cfg, err := loadConfig(filepath.Join(opts.DataDir, "config.json"), opts.DefaultName)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Node{
		opts:       opts,
		cfg:        cfg,
		seen:       map[string]seenDevice{},
		indexes:    map[string]map[string]FileInfo{},
		status:     map[string]*FolderStatus{},
		locks:      map[string]*sync.Mutex{},
		ctx:        ctx,
		cancel:     cancel,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Start opens the peer API, the local API and discovery.
func (n *Node) Start() error {
	// Peers learn our port from discovery and request headers, so any free port works.
	peerLn, err := n.listen("", n.opts.PeerPort, true)
	if err != nil {
		return fmt.Errorf("peer port: %w", err)
	}
	n.opts.PeerPort = peerLn.Addr().(*net.TCPAddr).Port
	host, port, err := net.SplitHostPort(n.opts.APIAddr)
	if err != nil {
		peerLn.Close()
		return fmt.Errorf("API address: %w", err)
	}
	p, _ := strconv.Atoi(port)
	apiLn, err := n.listen(host, p, n.opts.APIFallback)
	if err != nil {
		peerLn.Close()
		return fmt.Errorf("API address: %w", err)
	}
	n.apiURL = "http://" + apiLn.Addr().String()
	for ln, h := range map[net.Listener]http.Handler{peerLn: n.peerHandler(), apiLn: n.apiHandler()} {
		srv := &http.Server{Handler: h}
		n.servers = append(n.servers, srv)
		go srv.Serve(ln)
	}
	if n.opts.DiscoveryPort > 0 {
		n.startDiscovery()
	}
	n.logf("LanSync started: device %s (%s), peer port %d, UI %s", n.cfg.DeviceName, n.cfg.DeviceID, n.opts.PeerPort, n.apiURL)
	return nil
}

// listen opens a TCP listener on host:port. With fallback, a busy port makes it try the next ones.
func (n *Node) listen(host string, port int, fallback bool) (net.Listener, error) {
	tries := 1
	if fallback && port != 0 {
		tries = 20
	}
	var firstErr error
	for i := 0; i < tries; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+i)))
		if err == nil {
			if i > 0 {
				n.logf("port %d is in use, using %d instead", port, port+i)
			}
			return ln, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// Stop shuts the node down and cancels running transfers.
func (n *Node) Stop() {
	n.cancel()
	if n.udp != nil {
		n.udp.Close()
	}
	for _, s := range n.servers {
		s.Close()
	}
}

// APIURL is the base URL of the local REST API, e.g. "http://127.0.0.1:8384".
func (n *Node) APIURL() string { return n.apiURL }

func (n *Node) DeviceID() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cfg.DeviceID
}

func (n *Node) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	line := time.Now().Format("15:04:05 ") + msg
	n.mu.Lock()
	n.logLines = append(n.logLines, line)
	if len(n.logLines) > 300 {
		n.logLines = n.logLines[len(n.logLines)-300:]
	}
	n.mu.Unlock()
}

// saveConfigLocked persists n.cfg. Caller holds n.mu.
func (n *Node) saveConfigLocked() error {
	return writeJSON(filepath.Join(n.opts.DataDir, "config.json"), n.cfg)
}

func (n *Node) folder(id string) (Folder, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, f := range n.cfg.Folders {
		if f.ID == id {
			return f, true
		}
	}
	return Folder{}, false
}

func (n *Node) folderLock(id string) *sync.Mutex {
	n.mu.Lock()
	defer n.mu.Unlock()
	l, ok := n.locks[id]
	if !ok {
		l = &sync.Mutex{}
		n.locks[id] = l
	}
	return l
}

func (n *Node) folderStatus(id string) *FolderStatus {
	s, ok := n.status[id]
	if !ok {
		s = &FolderStatus{State: "idle"}
		n.status[id] = s
	}
	return s
}

func (n *Node) updateStatus(id string, fn func(s *FolderStatus)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	fn(n.folderStatus(id))
}

func (n *Node) indexPath(id string) string {
	return filepath.Join(n.opts.DataDir, "index", id+".json")
}

// loadIndex returns a copy of the folder's index, safe for the caller to modify.
func (n *Node) loadIndex(id string) map[string]FileInfo {
	n.mu.Lock()
	idx, ok := n.indexes[id]
	n.mu.Unlock()
	if !ok {
		idx = map[string]FileInfo{}
		var list []FileInfo
		if err := readJSON(n.indexPath(id), &list); err == nil {
			for _, f := range list {
				idx[f.Path] = f
			}
		}
		n.mu.Lock()
		n.indexes[id] = idx
		n.mu.Unlock()
	}
	cp := make(map[string]FileInfo, len(idx))
	for k, v := range idx {
		cp[k] = v
	}
	return cp
}

// storeIndex saves the index. It keeps its own copy, because the caller goes on changing idx
// while peers read the stored index concurrently.
func (n *Node) storeIndex(id string, idx map[string]FileInfo) {
	list := make([]FileInfo, 0, len(idx))
	stored := make(map[string]FileInfo, len(idx))
	var bytes int64
	for k, f := range idx {
		list = append(list, f)
		stored[k] = f
		bytes += f.Size
	}
	n.mu.Lock()
	n.indexes[id] = stored
	s := n.folderStatus(id)
	s.Files, s.Bytes = len(idx), bytes
	n.mu.Unlock()
	if err := writeJSON(n.indexPath(id), list); err != nil {
		n.logf("save index %s: %v", id, err)
	}
}

// addrOf returns the peer API address of a device: a recent discovery result first, then the manual address.
func (n *Node) addrOf(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.seen[id]; ok && time.Since(s.LastSeen) < 3*time.Minute {
		return s.Addr
	}
	for _, d := range n.cfg.Devices {
		if d.ID == id {
			return d.Addr
		}
	}
	return ""
}

func (n *Node) deviceName(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, d := range n.cfg.Devices {
		if d.ID == id && d.Name != "" {
			return d.Name
		}
	}
	if s, ok := n.seen[id]; ok {
		return s.Name
	}
	return id
}

func (n *Node) markSeen(id, name, addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id == "" || id == n.cfg.DeviceID {
		return
	}
	n.seen[id] = seenDevice{Name: name, Addr: addr, LastSeen: time.Now()}
	// Known devices show the name they chose for themselves, so renaming a device updates it everywhere.
	for i, d := range n.cfg.Devices {
		if d.ID == id && name != "" && d.Name != name {
			n.cfg.Devices[i].Name = name
			if err := n.saveConfigLocked(); err != nil {
				log.Printf("save config: %v", err)
			}
		}
	}
}
