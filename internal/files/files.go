package files

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type entry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	Modified int64  `json:"modified"`
	IsDir    bool   `json:"is_dir"`
	IsLink   bool   `json:"is_link"`
	Target   string `json:"target"`
}

type listResp struct {
	Path    string  `json:"path"`
	Parent  string  `json:"parent"`
	Entries []entry `json:"entries"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func validatePath(p string) error {
	if p == "" {
		return fmt.Errorf("path required")
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("path must be absolute")
	}
	cleaned := filepath.Clean(p)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("path must not contain '..'")
		}
	}
	// Deny explicit reads/writes of high-value secrets — file browser has
	// no business case for these, and bug-bounty class attackers reach
	// /etc/shadow / /etc/sudoers in seconds once a low-priv account exists.
	switch cleaned {
	case "/etc/shadow", "/etc/gshadow", "/etc/sudoers":
		return fmt.Errorf("path is on the deny list")
	}
	if strings.HasPrefix(cleaned, "/etc/sudoers.d/") ||
		strings.HasPrefix(cleaned, "/etc/ssh/") ||
		strings.HasPrefix(cleaned, "/root/.ssh/") ||
		strings.HasPrefix(cleaned, "/opt/panel/data/secrets") ||
		strings.HasPrefix(cleaned, "/opt/panel/data/.jwt") {
		return fmt.Errorf("path is on the deny list")
	}
	return nil
}

var protectedTopLevel = map[string]bool{
	"bin":  true,
	"etc":  true,
	"usr":  true,
	"var":  true,
	"boot": true,
	"proc": true,
	"sys":  true,
	"dev":  true,
	"root": true,
}

// isProtectedSymlinkTarget reports whether the resolved absolute target of a
// symlink lands inside a protectedTopLevel directory. Used to flag (not block)
// symlinks that point outside user-managed areas — UI shows "(protegido)" so
// users don't follow them silently into /etc, /root, etc.
func isProtectedSymlinkTarget(abs string) bool {
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	if len(parts) == 0 {
		return false
	}
	return protectedTopLevel[parts[0]]
}

// Handler returns an http.ServeMux with the file browser routes mounted at root.
func Handler() http.Handler {
	mux := http.NewServeMux()
	// Basics (CRUD)
	mux.HandleFunc("/list", handleList)
	mux.HandleFunc("/read", handleRead)
	mux.HandleFunc("/write", handleWrite)
	mux.HandleFunc("/mkdir", handleMkdir)
	mux.HandleFunc("/touch", handleTouch)
	mux.HandleFunc("/delete", handleDelete)
	mux.HandleFunc("/bulk-delete", handleBulkDelete)
	mux.HandleFunc("/rename", handleRename)
	mux.HandleFunc("/copy", handleCopy)
	mux.HandleFunc("/bulk-move", handleBulkMove)
	mux.HandleFunc("/download", handleDownload)
	mux.HandleFunc("/upload", handleUpload)
	// Compression
	mux.HandleFunc("/zip", handleZip)
	mux.HandleFunc("/tar", handleTar)
	mux.HandleFunc("/7z", handle7z)
	mux.HandleFunc("/extract", handleExtract)
	// Permissions
	mux.HandleFunc("/chmod", handleChmod)
	mux.HandleFunc("/chown", handleChown)
	// Trash
	mux.HandleFunc("/trash", handleTrash)
	mux.HandleFunc("/trash/list", handleTrashList)
	mux.HandleFunc("/trash/restore", handleTrashRestore)
	// Import from URL
	mux.HandleFunc("/fetch", handleFetch)
	// Metadata / analysis
	mux.HandleFunc("/properties", handleProperties)
	mux.HandleFunc("/dir-size", handleDirSize)
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/hash", handleHash)
	// Inline preview
	mux.HandleFunc("/preview", handlePreview)
	return mux
}

func handleList(w http.ResponseWriter, r *http.Request) {
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
	dirEntries, err := os.ReadDir(p)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		full := filepath.Join(p, de.Name())
		fi, err := os.Lstat(full)
		if err != nil {
			continue
		}
		e := entry{
			Name:     de.Name(),
			Size:     fi.Size(),
			Mode:     fi.Mode().String(),
			Modified: fi.ModTime().Unix(),
			IsDir:    fi.IsDir(),
			IsLink:   fi.Mode()&os.ModeSymlink != 0,
		}
		if e.IsLink {
			if tgt, err := os.Readlink(full); err == nil {
				// Resolve relative symlinks vs full dir; flag it when it points at
				// protectedTopLevel or outside any safe base.
				abs := tgt
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(p, abs)
				}
				abs = filepath.Clean(abs)
				e.Target = tgt
				// Mark symlinks pointing outside the current dir as "external" — the UI
				// already does not follow them automatically, but this avoids silent
				// confusion when one points at /etc/passwd and the like.
				if isProtectedSymlinkTarget(abs) {
					e.Target = tgt + " (protegido)"
				}
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := filepath.Dir(p)
	writeJSON(w, http.StatusOK, listResp{Path: p, Parent: parent, Entries: out})
}

func handleRead(w http.ResponseWriter, r *http.Request) {
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
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "is a directory")
		return
	}
	const maxSize = 2 * 1024 * 1024
	if fi.Size() > maxSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	sniff := make([]byte, 8192)
	n, _ := io.ReadFull(f, sniff)
	sniff = sniff[:n]
	for _, b := range sniff {
		if b == 0 {
			writeErr(w, http.StatusUnsupportedMediaType, "binary file")
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sniff)
	_, _ = io.Copy(w, f)
}

type writeReq struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func handleWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req writeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := filepath.Clean(req.Path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(p, []byte(req.Content), 0644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type pathReq struct {
	Path string `json:"path"`
}

func handleMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req pathReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := filepath.Clean(req.Path)
	if err := os.MkdirAll(p, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req pathReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := filepath.Clean(req.Path)
	if filepath.Dir(p) == "/" {
		base := filepath.Base(p)
		if protectedTopLevel[base] {
			writeErr(w, http.StatusForbidden, "refusing to delete protected top-level directory")
			return
		}
	}
	if err := os.RemoveAll(p); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type renameReq struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func handleRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req renameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.From); err != nil {
		writeErr(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	if err := validatePath(req.To); err != nil {
		writeErr(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	from := filepath.Clean(req.From)
	to := filepath.Clean(req.To)
	if err := os.Rename(from, to); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
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
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "is a directory")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	name := filepath.Base(p)
	// Explicit Content-Type BEFORE ServeContent: ServeContent only infers the
	// type from the extension when the header is not already set, and here the
	// intent is always "download", never "render in the browser".
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	// ServeContent in place of Content-Length + io.Copy: what used to be here
	// ALWAYS answered 200 with the entire file, ignoring the Range header, so an
	// interrupted download restarted from zero — on a bad connection a large
	// file never finished. ServeContent negotiates Range/If-Range/
	// If-Modified-Since, answers 206 with Content-Range and 416 on an impossible
	// range, and still sets the correct Content-Length in the common case.
	// We do not call WriteHeader beforehand: the one that decides the status
	// (200 or 206) is ServeContent.
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	dir := r.URL.Query().Get("path")
	if err := validatePath(dir); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dir = filepath.Clean(dir)
	fi, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "path is not a directory")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var file multipart.File
	var header *multipart.FileHeader
	file, header, err = r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if name == "" || name == "/" || name == "." || strings.Contains(name, "..") {
		writeErr(w, http.StatusBadRequest, "invalid filename")
		return
	}
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saved": dst})
}
