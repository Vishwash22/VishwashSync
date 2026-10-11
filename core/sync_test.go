package core_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lansync/core"
)

// peer is one running node with its synced folder at dir.
type peer struct {
	node *core.Node
	port int
	dir  string
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startPeer(t *testing.T, name string) *peer {
	t.Helper()
	port := freePort(t)
	n, err := core.New(core.Options{DataDir: t.TempDir(), DefaultName: name, APIAddr: "127.0.0.1:0", PeerPort: port, DiscoveryPort: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Stop)
	return &peer{node: n, port: port, dir: t.TempDir()}
}

func (p *peer) api(t *testing.T, method, path string, body any, out any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, p.node.APIURL()+path, bytes.NewReader(b))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		t.Fatalf("%s %s: %s %v", method, path, resp.Status, e)
	}
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
}

// link adds each peer as a device of the other and shares folder "F1" with the given modes,
// using the REST API the UIs use. B accepts A's folder through the offers list.
func link(t *testing.T, a, b *peer, modeA, modeB core.Mode) {
	t.Helper()
	a.api(t, "POST", "/api/devices", map[string]string{"id": b.node.DeviceID(), "name": "B", "addr": fmt.Sprintf("127.0.0.1:%d", b.port)}, nil)
	b.api(t, "POST", "/api/devices", map[string]string{"id": a.node.DeviceID(), "name": "A", "addr": fmt.Sprintf("127.0.0.1:%d", a.port)}, nil)
	a.api(t, "POST", "/api/folders", map[string]any{"id": "F1", "label": "Shared", "path": a.dir, "mode": modeA, "devices": []string{b.node.DeviceID()}}, nil)

	var offers []struct{ DeviceID, FolderID, Label string }
	b.api(t, "GET", "/api/offers", nil, &offers)
	if len(offers) != 1 || offers[0].FolderID != "F1" || offers[0].DeviceID != a.node.DeviceID() {
		t.Fatalf("offers = %+v, want F1 from A", offers)
	}
	b.api(t, "POST", "/api/folders", map[string]any{"id": "F1", "label": offers[0].Label, "path": b.dir, "mode": modeB, "devices": []string{a.node.DeviceID()}}, nil)
}

func syncNow(t *testing.T, p *peer) {
	t.Helper()
	if err := p.node.SyncFolder("F1", false); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func write(t *testing.T, dir, rel, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mtime, mtime)
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// conflictCopies returns the contents of all conflict copies of base (e.g. "notes" for notes.txt).
func conflictCopies(t *testing.T, dir, base string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, base+".sync-conflict-*"))
	var out []string
	for _, p := range m {
		b, _ := os.ReadFile(p)
		out = append(out, string(b))
	}
	return out
}

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestTwoWaySync(t *testing.T) {
	a, b := startPeer(t, "A"), startPeer(t, "B")
	link(t, a, b, core.SendReceive, core.SendReceive)
	write(t, a.dir, "a.txt", "from A", t0)
	write(t, b.dir, "sub/dir/b.txt", "from B", t0)

	syncNow(t, a)
	syncNow(t, b)
	for _, p := range []*peer{a, b} {
		if got := read(t, p.dir, "a.txt"); got != "from A" {
			t.Errorf("a.txt = %q", got)
		}
		if got := read(t, p.dir, "sub/dir/b.txt"); got != "from B" {
			t.Errorf("sub/dir/b.txt = %q", got)
		}
	}
	st, _ := os.Stat(filepath.Join(b.dir, "a.txt"))
	if !st.ModTime().Equal(t0) {
		t.Errorf("mtime not preserved: %v", st.ModTime())
	}

	// A normal edit (not a conflict) on B replaces A's copy without conflict copies.
	write(t, b.dir, "a.txt", "edited on B", t0.Add(time.Hour))
	syncNow(t, a)
	if got := read(t, a.dir, "a.txt"); got != "edited on B" {
		t.Errorf("after edit a.txt = %q", got)
	}
	if c := conflictCopies(t, a.dir, "a"); len(c) != 0 {
		t.Errorf("unexpected conflict copies %v", c)
	}

	// Deletes are not propagated; a deleted file comes back while a peer still has it.
	os.Remove(filepath.Join(a.dir, "a.txt"))
	syncNow(t, b)
	if got := read(t, b.dir, "a.txt"); got != "edited on B" {
		t.Errorf("delete propagated to B: %q", got)
	}
	syncNow(t, a)
	if got := read(t, a.dir, "a.txt"); got != "edited on B" {
		t.Errorf("deleted file not restored on A: %q", got)
	}
}

func TestSendReceiveConflictNewestWins(t *testing.T) {
	a, b := startPeer(t, "A"), startPeer(t, "B")
	link(t, a, b, core.SendReceive, core.SendReceive)
	write(t, a.dir, "notes.txt", "v1", t0)
	syncNow(t, b)

	// Both edit; B's edit is older than A's.
	write(t, a.dir, "notes.txt", "A edit (newer)", t0.Add(2*time.Hour))
	write(t, b.dir, "notes.txt", "B edit (older)", t0.Add(time.Hour))
	syncNow(t, b) // B pulls first: A's newer version wins, B's edit becomes a conflict copy
	syncNow(t, a) // A pulls the conflict copy
	syncNow(t, b)

	for name, p := range map[string]*peer{"A": a, "B": b} {
		if got := read(t, p.dir, "notes.txt"); got != "A edit (newer)" {
			t.Errorf("%s: notes.txt = %q, want newer edit", name, got)
		}
		if c := conflictCopies(t, p.dir, "notes"); len(c) != 1 || c[0] != "B edit (older)" {
			t.Errorf("%s: conflict copies = %q, want the older edit", name, c)
		}
	}
}

// Phone (send-only) -> desktop (receive-only), the case from the spec discussion.
func TestOneWaySync(t *testing.T) {
	phone, desktop := startPeer(t, "phone"), startPeer(t, "desktop")
	link(t, phone, desktop, core.SendOnly, core.ReceiveOnly)
	write(t, phone.dir, "photo.jpg", "photo v1", t0)
	write(t, phone.dir, "doc.txt", "doc v1", t0)
	syncNow(t, desktop)
	if got := read(t, desktop.dir, "photo.jpg"); got != "photo v1" {
		t.Fatalf("photo.jpg on desktop = %q", got)
	}

	// Deleting on the phone keeps the desktop copy, and the phone does not get it back.
	os.Remove(filepath.Join(phone.dir, "photo.jpg"))
	syncNow(t, phone)
	syncNow(t, desktop)
	if got := read(t, desktop.dir, "photo.jpg"); got != "photo v1" {
		t.Errorf("desktop lost photo.jpg: %q", got)
	}
	if got := read(t, phone.dir, "photo.jpg"); got != "<missing>" {
		t.Errorf("phone got deleted photo back: %q", got)
	}

	// A desktop-only edit stays on the desktop.
	write(t, desktop.dir, "doc.txt", "desktop edit", t0.Add(time.Hour))
	syncNow(t, desktop)
	if got := read(t, desktop.dir, "doc.txt"); got != "desktop edit" {
		t.Errorf("desktop edit reverted: %q", got)
	}

	// Both edit: the phone wins even though the desktop edit is newer; the desktop edit is kept as a copy.
	write(t, phone.dir, "doc.txt", "phone edit", t0.Add(2*time.Hour))
	write(t, desktop.dir, "doc.txt", "desktop edit 2", t0.Add(3*time.Hour))
	syncNow(t, desktop)
	if got := read(t, desktop.dir, "doc.txt"); got != "phone edit" {
		t.Errorf("desktop doc.txt = %q, want phone edit", got)
	}
	if c := conflictCopies(t, desktop.dir, "doc"); len(c) != 1 || c[0] != "desktop edit 2" {
		t.Errorf("desktop conflict copies = %q", c)
	}
	syncNow(t, phone)
	if c := conflictCopies(t, phone.dir, "doc"); len(c) != 0 {
		t.Errorf("conflict copy leaked to phone: %q", c)
	}
	entries, _ := os.ReadDir(desktop.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".lansync-tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// A receive-only folder never offers its local changes, even to a peer that pulls.
func TestReceiveOnlyKeepsLocalChangesLocal(t *testing.T) {
	laptop, desktop := startPeer(t, "laptop"), startPeer(t, "desktop")
	link(t, laptop, desktop, core.SendReceive, core.ReceiveOnly)
	write(t, laptop.dir, "doc.txt", "v1", t0)
	syncNow(t, desktop)

	write(t, desktop.dir, "doc.txt", "desktop edit", t0.Add(time.Hour))
	write(t, desktop.dir, "desktop-only.txt", "local", t0)
	syncNow(t, desktop)
	syncNow(t, laptop)
	if got := read(t, laptop.dir, "doc.txt"); got != "v1" {
		t.Errorf("laptop received desktop edit: %q", got)
	}
	if got := read(t, laptop.dir, "desktop-only.txt"); got != "<missing>" {
		t.Errorf("laptop received desktop-only file")
	}
	if got := read(t, desktop.dir, "doc.txt"); got != "desktop edit" {
		t.Errorf("desktop edit reverted: %q", got)
	}
}

// A port already used by another program must not stop LanSync from starting.
func TestBusyPeerPortFallsBack(t *testing.T) {
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	n, err := core.New(core.Options{DataDir: t.TempDir(), APIAddr: "127.0.0.1:0", PeerPort: port, DiscoveryPort: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(); err != nil {
		t.Fatalf("start with busy port: %v", err)
	}
	t.Cleanup(n.Stop)
	var st struct{ PeerPort int }
	(&peer{node: n}).api(t, "GET", "/api/status", nil, &st)
	if st.PeerPort == port || st.PeerPort == 0 {
		t.Fatalf("peer port = %d, want a free port other than %d", st.PeerPort, port)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/peer/hello", st.PeerPort))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("peer API not reachable on reported port: %v", err)
	}
	resp.Body.Close()
}
