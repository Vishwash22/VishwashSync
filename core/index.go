package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Vector is a version vector: how many times each device has changed a file.
// Comparing two vectors tells a normal update apart from a conflict.
type Vector map[string]uint64

type order int

const (
	equal order = iota
	greater
	lesser
	concurrent
)

func (a Vector) compare(b Vector) order {
	gt, lt := false, false
	for k, av := range a {
		if av > b[k] {
			gt = true
		} else if av < b[k] {
			lt = true
		}
	}
	for k, bv := range b {
		if _, ok := a[k]; !ok && bv > 0 {
			lt = true
		}
	}
	switch {
	case gt && lt:
		return concurrent
	case gt:
		return greater
	case lt:
		return lesser
	}
	return equal
}

func (a Vector) merge(b Vector) Vector {
	m := Vector{}
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		if v > m[k] {
			m[k] = v
		}
	}
	return m
}

func (a Vector) bump(id string) Vector {
	m := a.merge(nil)
	m[id]++
	return m
}

// FileInfo is one entry of a folder index.
type FileInfo struct {
	Path    string `json:"path"` // slash-separated, relative to the folder root
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"` // unix nanoseconds
	Hash    string `json:"hash"`    // hex SHA-256
	Version Vector `json:"version"`
	// LocalChange marks a file changed locally in a receive-only folder. Such files are never offered to peers.
	LocalChange bool `json:"localChange,omitempty"`
}

const tempPrefix = ".lansync-tmp-"

func isTemp(name string) bool { return strings.HasPrefix(name, tempPrefix) }

// validRel reports whether p is a safe relative slash path (no "..", not absolute).
func validRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, ":") {
		return false
	}
	return path.Clean(p) == p && p != "." && !strings.HasPrefix(p, "../") && p != ".."
}

// scan walks root and returns the new index. Unchanged files (same size and mtime) are not re-hashed.
// Changed or new files get this device's version counter bumped. Files that disappeared are dropped,
// so deletes are never sent to peers (and a deleted file is pulled again if a peer still has it).
func scan(root string, mode Mode, self string, old map[string]FileInfo) (map[string]FileInfo, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("folder path not available: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("folder path %q is not a directory", root)
	}
	out := map[string]FileInfo{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // skip unreadable entries
		}
		if !d.Type().IsRegular() || isTemp(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		mt := info.ModTime().UnixNano()
		o, had := old[rel]
		if had && o.Size == info.Size() && o.ModTime == mt {
			out[rel] = o
			return nil
		}
		h, err := hashFile(p)
		if err != nil {
			return nil // e.g. file in use; picked up next time
		}
		f := FileInfo{Path: rel, Size: info.Size(), ModTime: mt, Hash: h}
		if had && o.Hash == h {
			f.Version, f.LocalChange = o.Version, o.LocalChange
		} else {
			f.Version = o.Version.bump(self)
			f.LocalChange = mode == ReceiveOnly
		}
		out[rel] = f
		return nil
	})
	return out, err
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// conflictName turns "dir/report.txt" into "dir/report.sync-conflict-20261011-153000-ABCDEFG.txt".
func conflictName(rel, device string, t time.Time) string {
	dir, file := path.Split(rel)
	ext := path.Ext(file)
	base := strings.TrimSuffix(file, ext)
	short := strings.ReplaceAll(device, "-", "")
	if len(short) > 7 {
		short = short[:7]
	}
	return dir + base + ".sync-conflict-" + t.Format("20060102-150405") + "-" + short + ext
}
