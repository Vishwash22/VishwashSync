package core

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Folder browser for the web UI: browsers can't reveal real folder paths, so the UI asks the
// (local) core to list directories instead.

type place struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type browseResult struct {
	Path   string  `json:"path"`
	Parent string  `json:"parent"` // empty at a filesystem root
	Sep    string  `json:"sep"`
	Dirs   []place `json:"dirs"`
	Places []place `json:"places"` // home folders and drives
}

func (n *Node) handleBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p, _ = os.UserHomeDir()
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" && len(p) == 2 && p[1] == ':' {
		p += `\` // "C:" means the current directory on C:, not its root
	}
	entries, err := os.ReadDir(p)
	// A suggested folder may not exist yet: show the nearest existing parent instead.
	for errors.Is(err, os.ErrNotExist) && filepath.Dir(p) != p {
		p = filepath.Dir(p)
		entries, err = os.ReadDir(p)
	}
	if err != nil {
		httpError(w, err)
		return
	}
	res := browseResult{Path: p, Sep: string(filepath.Separator), Dirs: []place{}, Places: browsePlaces()}
	if parent := filepath.Dir(p); parent != p {
		res.Parent = parent
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
			continue
		}
		full := filepath.Join(p, name)
		if !e.IsDir() {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if st, err := os.Stat(full); err != nil || !st.IsDir() {
				continue
			}
		}
		res.Dirs = append(res.Dirs, place{name, full})
	}
	sort.Slice(res.Dirs, func(i, j int) bool { return strings.ToLower(res.Dirs[i].Name) < strings.ToLower(res.Dirs[j].Name) })
	writeJSONResponse(w, res)
}

func browsePlaces() []place {
	var out []place
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, place{"Home", home})
		for _, name := range []string{"Desktop", "Documents", "Pictures", "Music", "Videos", "Downloads"} {
			if st, err := os.Stat(filepath.Join(home, name)); err == nil && st.IsDir() {
				out = append(out, place{name, filepath.Join(home, name)})
			}
		}
	}
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			root := string(c) + `:\`
			if _, err := os.Stat(root); err == nil {
				out = append(out, place{string(c) + ":", root})
			}
		}
	} else {
		out = append(out, place{"Computer", "/"})
	}
	return out
}
