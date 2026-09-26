package files

import (
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ─── PROPERTIES ────────────────────────────────────────────────

type properties struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	IsDir     bool   `json:"is_dir"`
	IsLink    bool   `json:"is_link"`
	LinkTo    string `json:"link_to,omitempty"`
	ModeStr   string `json:"mode_str"`   // e.g. "-rw-r--r--"
	ModeOctal string `json:"mode_octal"` // e.g. "0644"
	Modified  int64  `json:"modified"`
	Accessed  int64  `json:"accessed"`
	Changed   int64  `json:"changed"`
	UID       uint32 `json:"uid"`
	GID       uint32 `json:"gid"`
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Files     int64  `json:"files,omitempty"`   // only if dir
	Subdirs   int64  `json:"subdirs,omitempty"` // only if dir
}

func handleProperties(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	p := r.URL.Query().Get("path")
	if err := validatePath(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p = filepath.Clean(p)
	fi, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	prop := properties{
		Path:      p,
		Name:      filepath.Base(p),
		Size:      fi.Size(),
		IsDir:     fi.IsDir(),
		IsLink:    fi.Mode()&os.ModeSymlink != 0,
		ModeStr:   fi.Mode().String(),
		ModeOctal: fmt.Sprintf("0%o", fi.Mode().Perm()),
		Modified:  fi.ModTime().Unix(),
	}
	if prop.IsLink {
		if t, err := os.Readlink(p); err == nil {
			prop.LinkTo = t
		}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		prop.UID = st.Uid
		prop.GID = st.Gid
		prop.Accessed = st.Atim.Sec
		prop.Changed = st.Ctim.Sec
		if u, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10)); err == nil {
			prop.Owner = u.Username
		}
		if g, err := user.LookupGroupId(strconv.FormatUint(uint64(st.Gid), 10)); err == nil {
			prop.Group = g.Name
		}
	}
	if prop.IsDir {
		var nf, nd int64
		_ = filepath.WalkDir(p, func(sub string, d os.DirEntry, werr error) error {
			if werr != nil || sub == p {
				return nil
			}
			if d.IsDir() {
				nd++
			} else {
				nf++
			}
			return nil
		})
		prop.Files = nf
		prop.Subdirs = nd
	}
	writeJSON(w, http.StatusOK, prop)
}

// ─── DU (total size of a folder) ───────────────────────────────

func handleDirSize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	p := r.URL.Query().Get("path")
	if err := validatePath(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p = filepath.Clean(p)
	var total int64
	var count int64
	err := filepath.WalkDir(p, func(sub string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil // ignore isolated errors (perms etc)
		}
		if !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
				count++
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "size": total, "files": count})
}

// ─── SEARCH (name and/or content) ─────────────────────────────

type searchHit struct {
	Path    string `json:"path"`
	Snippet string `json:"snippet,omitempty"`
	Line    int    `json:"line,omitempty"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	root := r.URL.Query().Get("root")
	name := r.URL.Query().Get("name")       // substring (case-insensitive) or empty
	content := r.URL.Query().Get("content") // substring in the body (case-sensitive)
	maxStr := r.URL.Query().Get("max")
	max := 200
	if maxStr != "" {
		if n, err := strconv.Atoi(maxStr); err == nil && n > 0 && n <= 2000 {
			max = n
		}
	}
	if err := validatePath(root); err != nil {
		writeErr(w, http.StatusBadRequest, "root: "+err.Error())
		return
	}
	root = filepath.Clean(root)
	if name == "" && content == "" {
		writeErr(w, http.StatusBadRequest, "provide name and/or content")
		return
	}
	nameLow := strings.ToLower(name)
	hits := []searchHit{}
	stop := false
	_ = filepath.WalkDir(root, func(sub string, d os.DirEntry, werr error) error {
		if werr != nil || stop {
			return nil
		}
		// Skip obviously expensive folders (light heuristic).
		if d.IsDir() {
			base := filepath.Base(sub)
			if base == "node_modules" || base == ".git" || base == ".next" || base == "pg_wal" {
				return filepath.SkipDir
			}
			if nameLow != "" && strings.Contains(strings.ToLower(base), nameLow) {
				hits = append(hits, searchHit{Path: sub, IsDir: true})
				if len(hits) >= max {
					stop = true
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		matched := false
		if nameLow != "" {
			if strings.Contains(strings.ToLower(d.Name()), nameLow) {
				matched = true
			}
		}
		if content != "" && info.Size() > 0 && info.Size() < 2*1024*1024 {
			// Only search content inside small files (<2MB).
			b, err := os.ReadFile(sub)
			if err == nil {
				if idx := strings.Index(string(b), content); idx >= 0 {
					line := 1
					for _, c := range string(b[:idx]) {
						if c == '\n' {
							line++
						}
					}
					start := idx - 40
					if start < 0 {
						start = 0
					}
					end := idx + len(content) + 40
					if end > len(b) {
						end = len(b)
					}
					hits = append(hits, searchHit{
						Path:    sub,
						Snippet: strings.ReplaceAll(string(b[start:end]), "\n", " "),
						Line:    line,
						Size:    info.Size(),
					})
					matched = false // already added
				}
			}
		}
		if matched {
			hits = append(hits, searchHit{Path: sub, Size: info.Size()})
		}
		if len(hits) >= max {
			stop = true
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "count": len(hits), "limit": max})
}
