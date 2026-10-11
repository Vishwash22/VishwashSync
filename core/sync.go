package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SyncFolder rescans a folder and pulls changes from every device it is shared with (unless the
// folder is send-only). With notify, it then asks those devices to sync too and waits until they
// have finished, so one press of "Sync now" syncs both directions and only reports done when the
// other devices are done. If the folder is already syncing, it waits and then syncs again.
func (n *Node) SyncFolder(id string, notify bool) error {
	f, ok := n.folder(id)
	if !ok {
		return fmt.Errorf("unknown folder %q", id)
	}
	l := n.folderLock(id)
	l.Lock()
	err := n.syncLocked(f)
	l.Unlock() // peers pull from us while we wait for them below
	if err == nil && notify {
		err = n.notifyPeers(f)
	}
	n.updateStatus(id, func(s *FolderStatus) {
		s.Done, s.Total, s.Waiting = 0, 0, ""
		if err != nil {
			s.State, s.LastError = "error", err.Error()
		} else {
			s.State, s.LastError, s.LastSync = "idle", "", time.Now()
		}
	})
	if err != nil {
		n.logf("%s: sync failed: %v", f.Label, err)
		return err
	}
	n.logf("%s: sync finished", f.Label)
	return nil
}

// notifyPeers asks each device sharing the folder to sync and waits for it to finish, showing
// that device's progress in this folder's status meanwhile.
func (n *Node) notifyPeers(f Folder) error {
	reached := 0
	var errs []string
	for _, dev := range f.Devices {
		addr := n.addrOf(dev)
		if addr == "" {
			continue
		}
		name := n.deviceName(dev)
		n.updateStatus(f.ID, func(s *FolderStatus) { s.State, s.Waiting, s.Done, s.Total = "syncing", name, 0, 0 })
		stop := make(chan struct{})
		go n.followPeer(f.ID, addr, stop)
		err := n.requestPeerSync(f.ID, addr)
		close(stop)
		if err != nil {
			n.logf("%s: %s did not finish syncing: %v", f.Label, name, err)
			errs = append(errs, name+": "+err.Error())
			continue
		}
		reached++
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	if reached == 0 && len(f.Devices) > 0 && f.Mode == SendOnly {
		return errors.New("none of the devices sharing this folder are reachable")
	}
	return nil
}

func (n *Node) requestPeerSync(folderID, addr string) error {
	req, err := n.peerRequest("POST", addr, "/peer/folders/"+url.PathEscape(folderID)+"/sync-request", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) // no timeout: the peer may be downloading for a long time
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return errors.New(strings.TrimSpace(string(msg)))
	}
	return nil
}

// followPeer mirrors the peer's download progress into our folder status until stop is closed.
func (n *Node) followPeer(folderID, addr string, stop <-chan struct{}) {
	t := time.NewTicker(700 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		var st FolderStatus
		if n.peerGetJSON(addr, "/peer/folders/"+url.PathEscape(folderID)+"/status", &st) == nil {
			n.updateStatus(folderID, func(s *FolderStatus) {
				if s.Waiting != "" {
					s.Done, s.Total = st.Done, st.Total
				}
			})
		}
	}
}

func (n *Node) syncLocked(f Folder) error {
	n.updateStatus(f.ID, func(s *FolderStatus) { s.State, s.LastError = "scanning", "" })
	idx, err := n.rescan(f)
	if err != nil {
		return err
	}
	if f.Mode == SendOnly {
		return nil
	}
	n.updateStatus(f.ID, func(s *FolderStatus) { s.State = "syncing" })
	reached := 0
	for _, dev := range f.Devices {
		name := n.deviceName(dev)
		addr := n.addrOf(dev)
		if addr == "" {
			n.logf("%s: %s is offline", f.Label, name)
			continue
		}
		var remote []FileInfo
		if err := n.peerGetJSON(addr, "/peer/folders/"+url.PathEscape(f.ID)+"/index", &remote); err != nil {
			n.logf("%s: cannot reach %s: %v", f.Label, name, err)
			continue
		}
		reached++
		n.pull(f, idx, dev, addr, remote)
		n.storeIndex(f.ID, idx)
	}
	if reached == 0 && len(f.Devices) > 0 {
		return errors.New("none of the devices sharing this folder are reachable")
	}
	return nil
}

// rescan updates the folder index from disk. Caller holds the folder lock.
func (n *Node) rescan(f Folder) (map[string]FileInfo, error) {
	idx, err := scan(f.Path, f.Mode, n.DeviceID(), n.loadIndex(f.ID))
	if err != nil {
		return nil, err
	}
	n.storeIndex(f.ID, idx)
	return idx, nil
}

type pullJob struct {
	r          FileInfo
	local      *FileInfo // nil if we don't have the file
	conflict   bool
	remoteWins bool
}

// pull compares a peer's index with ours and downloads what we need, updating idx in place.
func (n *Node) pull(f Folder, idx map[string]FileInfo, dev, addr string, remote []FileInfo) {
	self := n.DeviceID()
	var jobs []pullJob
	for _, r := range remote {
		if !validRel(r.Path) || isTemp(path.Base(r.Path)) || r.Hash == "" {
			continue
		}
		l, have := idx[r.Path]
		if !have {
			jobs = append(jobs, pullJob{r: r})
			continue
		}
		switch r.Version.compare(l.Version) {
		case greater:
			if l.Hash == r.Hash {
				l.Version, l.LocalChange = r.Version, false
				idx[r.Path] = l
				continue
			}
			jobs = append(jobs, pullJob{r: r, local: &l})
		case concurrent:
			if l.Hash == r.Hash {
				l.Version, l.LocalChange = l.Version.merge(r.Version), false
				idx[r.Path] = l
				continue
			}
			// Receive-only folders always take the sender's version. Otherwise the newest file wins.
			remoteWins := f.Mode == ReceiveOnly || r.ModTime > l.ModTime || (r.ModTime == l.ModTime && dev > self)
			jobs = append(jobs, pullJob{r: r, local: &l, conflict: true, remoteWins: remoteWins})
		}
	}
	if len(jobs) == 0 {
		return
	}
	n.updateStatus(f.ID, func(s *FolderStatus) { s.Total += len(jobs) })
	name := n.deviceName(dev)
	for _, j := range jobs {
		if n.ctx.Err() != nil {
			return
		}
		if err := n.apply(f, idx, self, dev, addr, j); err != nil {
			n.logf("%s: %s: %v", f.Label, j.r.Path, err)
		} else if j.conflict {
			n.logf("%s: conflict on %s with %s; kept both versions", f.Label, j.r.Path, name)
		} else {
			n.logf("%s: received %s from %s", f.Label, j.r.Path, name)
		}
		n.updateStatus(f.ID, func(s *FolderStatus) { s.Done++ })
	}
}

func (n *Node) apply(f Folder, idx map[string]FileInfo, self, dev, addr string, j pullJob) error {
	r := j.r
	switch {
	case !j.conflict:
		fi, err := n.download(f, addr, r, r.Path, j.local)
		if err != nil {
			return err
		}
		fi.Version = r.Version
		idx[r.Path] = fi
	case j.remoteWins:
		// Move our version aside as a conflict copy, then take theirs.
		if err := checkUnchanged(f, r.Path, j.local); err != nil {
			return err
		}
		copyRel := conflictName(r.Path, self, time.Now())
		if err := os.Rename(absPath(f, r.Path), absPath(f, copyRel)); err != nil {
			return err
		}
		fi, err := n.download(f, addr, r, r.Path, nil)
		if err != nil {
			return err // our version is safe in the conflict copy
		}
		fi.Version = r.Version.merge(j.local.Version)
		idx[r.Path] = fi
	default:
		// Ours wins: save theirs as a conflict copy and mark our version as newer than both.
		if _, err := n.download(f, addr, r, conflictName(r.Path, dev, time.Now()), nil); err != nil {
			return err
		}
		l := *j.local
		l.Version = l.Version.merge(r.Version)
		idx[r.Path] = l
	}
	return nil
}

func absPath(f Folder, rel string) string { return filepath.Join(f.Path, filepath.FromSlash(rel)) }

// checkUnchanged makes sure the file on disk still matches what the scan saw (expect nil: must not exist),
// so a file edited while we were syncing is never overwritten. It is picked up by the next sync instead.
func checkUnchanged(f Folder, rel string, expect *FileInfo) error {
	st, err := os.Stat(absPath(f, rel))
	switch {
	case expect == nil && errors.Is(err, os.ErrNotExist):
		return nil
	case expect != nil && err == nil && st.Size() == expect.Size && st.ModTime().UnixNano() == expect.ModTime:
		return nil
	}
	return errors.New("file changed locally during sync; will retry next sync")
}

// download fetches r from the peer into destRel via a temp file, verifies its hash, restores its
// modification time and renames it into place.
func (n *Node) download(f Folder, addr string, r FileInfo, destRel string, expect *FileInfo) (FileInfo, error) {
	dest := absPath(f, destRel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return FileInfo{}, err
	}
	req, err := n.peerRequest("GET", addr, "/peer/folders/"+url.PathEscape(f.ID)+"/file", url.Values{"path": {r.Path}})
	if err != nil {
		return FileInfo{}, err
	}
	resp, err := http.DefaultClient.Do(req) // no timeout: large files; cancelled via the node context
	if err != nil {
		return FileInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return FileInfo{}, fmt.Errorf("download: %s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), tempPrefix+"*")
	if err != nil {
		return FileInfo{}, err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return FileInfo{}, err
	}
	if hex.EncodeToString(h.Sum(nil)) != r.Hash {
		return FileInfo{}, errors.New("file changed on the other device during transfer; will retry next sync")
	}
	mt := time.Unix(0, r.ModTime)
	if err := os.Chtimes(tmp.Name(), mt, mt); err != nil {
		return FileInfo{}, err
	}
	if err := checkUnchanged(f, destRel, expect); err != nil {
		return FileInfo{}, err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return FileInfo{}, err
	}
	st, err := os.Stat(dest)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Path: destRel, Size: st.Size(), ModTime: st.ModTime().UnixNano(), Hash: r.Hash}, nil
}
