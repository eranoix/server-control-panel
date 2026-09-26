package files

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─── COPY (recursive) ──────────────────────────────────────────

type copyReq struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func handleCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req copyReq
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
	if err := copyRecursive(from, to); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func copyRecursive(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyRecursive(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ─── BULK DELETE / MOVE ────────────────────────────────────────

type bulkPathsReq struct {
	Paths []string `json:"paths"`
}

func handleBulkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req bulkPathsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "paths required")
		return
	}
	results := make([]map[string]any, 0, len(req.Paths))
	for _, p := range req.Paths {
		if err := validatePath(p); err != nil {
			results = append(results, map[string]any{"path": p, "ok": false, "error": err.Error()})
			continue
		}
		clean := filepath.Clean(p)
		if filepath.Dir(clean) == "/" {
			base := filepath.Base(clean)
			if protectedTopLevel[base] {
				results = append(results, map[string]any{"path": clean, "ok": false, "error": "protected top-level"})
				continue
			}
		}
		if err := os.RemoveAll(clean); err != nil {
			results = append(results, map[string]any{"path": clean, "ok": false, "error": err.Error()})
			continue
		}
		results = append(results, map[string]any{"path": clean, "ok": true})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}

type bulkMoveReq struct {
	Paths   []string `json:"paths"`
	DestDir string   `json:"dest_dir"`
}

func handleBulkMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req bulkMoveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.DestDir); err != nil {
		writeErr(w, http.StatusBadRequest, "dest_dir: "+err.Error())
		return
	}
	destDir := filepath.Clean(req.DestDir)
	if info, err := os.Stat(destDir); err != nil || !info.IsDir() {
		writeErr(w, http.StatusBadRequest, "dest_dir is not a directory")
		return
	}
	results := make([]map[string]any, 0, len(req.Paths))
	for _, p := range req.Paths {
		if err := validatePath(p); err != nil {
			results = append(results, map[string]any{"path": p, "ok": false, "error": err.Error()})
			continue
		}
		from := filepath.Clean(p)
		to := filepath.Join(destDir, filepath.Base(from))
		if err := os.Rename(from, to); err != nil {
			results = append(results, map[string]any{"path": from, "ok": false, "error": err.Error()})
			continue
		}
		results = append(results, map[string]any{"path": from, "to": to, "ok": true})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}

// ─── CHMOD / CHOWN ─────────────────────────────────────────────

type chmodReq struct {
	Path      string `json:"path"`
	Mode      string `json:"mode"` // octal "0644" or "644"
	Recursive bool   `json:"recursive"`
}

func handleChmod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req chmodReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mstr := strings.TrimPrefix(req.Mode, "0")
	if mstr == "" {
		writeErr(w, http.StatusBadRequest, "mode required (ex: 0644)")
		return
	}
	m64, err := strconv.ParseUint(mstr, 8, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid mode: "+err.Error())
		return
	}
	mode := os.FileMode(m64)
	p := filepath.Clean(req.Path)
	if req.Recursive {
		err = filepath.Walk(p, func(sub string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			return os.Chmod(sub, mode)
		})
	} else {
		err = os.Chmod(p, mode)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": fmt.Sprintf("%o", mode)})
}

type chownReq struct {
	Path      string `json:"path"`
	UID       int    `json:"uid"`
	GID       int    `json:"gid"`
	Recursive bool   `json:"recursive"`
}

func handleChown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req chownReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := filepath.Clean(req.Path)
	var err error
	if req.Recursive {
		err = filepath.Walk(p, func(sub string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			return os.Chown(sub, req.UID, req.GID)
		})
	} else {
		err = os.Chown(p, req.UID, req.GID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ─── TRASH / RESTORE ───────────────────────────────────────────

const trashDir = "/root/.vpsm-trash"

func ensureTrash() error { return os.MkdirAll(trashDir, 0700) }

func handleTrash(w http.ResponseWriter, r *http.Request) {
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
	if err := ensureTrash(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	src := filepath.Clean(req.Path)
	if filepath.Dir(src) == "/" {
		base := filepath.Base(src)
		if protectedTopLevel[base] {
			writeErr(w, http.StatusForbidden, "refusing to trash protected top-level directory")
			return
		}
	}
	ts := time.Now().UTC().Format("20060102-150405.000")
	// Encodes the original path into the name (base64-url-like) so it can be restored later.
	orig := strings.ReplaceAll(strings.TrimPrefix(src, "/"), "/", "_SLASH_")
	dst := filepath.Join(trashDir, fmt.Sprintf("%s__%s", ts, orig))
	if err := os.Rename(src, dst); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trash_path": dst, "original": src})
}

type trashEntry struct {
	TrashPath string `json:"trash_path"`
	Original  string `json:"original"`
	When      string `json:"when"`
}

func handleTrashList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	if err := ensureTrash(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	es, err := os.ReadDir(trashDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]trashEntry, 0, len(es))
	for _, e := range es {
		name := e.Name()
		parts := strings.SplitN(name, "__", 2)
		if len(parts) != 2 {
			continue
		}
		orig := "/" + strings.ReplaceAll(parts[1], "_SLASH_", "/")
		out = append(out, trashEntry{
			TrashPath: filepath.Join(trashDir, name),
			Original:  orig,
			When:      parts[0],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

type restoreReq struct {
	TrashPath string `json:"trash_path"`
	Dest      string `json:"dest,omitempty"` // if empty, restores to the original location (extracted from the name)
}

func handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req restoreReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.HasPrefix(filepath.Clean(req.TrashPath), trashDir+"/") {
		writeErr(w, http.StatusBadRequest, "trash_path is outside the trash directory")
		return
	}
	dest := req.Dest
	if dest == "" {
		base := filepath.Base(req.TrashPath)
		parts := strings.SplitN(base, "__", 2)
		if len(parts) != 2 {
			writeErr(w, http.StatusBadRequest, "could not infer the original destination")
			return
		}
		dest = "/" + strings.ReplaceAll(parts[1], "_SLASH_", "/")
	}
	if err := validatePath(dest); err != nil {
		writeErr(w, http.StatusBadRequest, "dest: "+err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(req.TrashPath, dest); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restored": dest})
}

// ─── FETCH (import from URL) ───────────────────────────────────

type fetchReq struct {
	URL      string `json:"url"`
	Dest     string `json:"dest"`               // destination directory
	Filename string `json:"filename,omitempty"` // optional name; default = last segment of the URL
}

func handleFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req fetchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Dest); err != nil {
		writeErr(w, http.StatusBadRequest, "dest: "+err.Error())
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeErr(w, http.StatusBadRequest, "invalid url (http/https)")
		return
	}
	name := req.Filename
	if name == "" {
		name = filepath.Base(u.Path)
		if name == "" || name == "/" || name == "." {
			name = "download"
		}
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		writeErr(w, http.StatusBadRequest, "invalid filename")
		return
	}
	destDir := filepath.Clean(req.Dest)
	if info, e := os.Stat(destDir); e != nil || !info.IsDir() {
		writeErr(w, http.StatusBadRequest, "dest is not a directory")
		return
	}
	full := filepath.Join(destDir, name)
	// Cap as a defense against slowloris, or against a server streaming an
	// open-ended GB. 2GB is generous for an ISO/backup and still protects the disk.
	const maxDownloadBytes = 2 << 30 // 2 GiB
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(u.String())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return
	}
	if resp.ContentLength > maxDownloadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("download larger than the %d byte cap", maxDownloadBytes))
		return
	}
	out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer out.Close()
	limited := io.LimitReader(resp.Body, maxDownloadBytes+1)
	n, err := io.Copy(out, limited)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > maxDownloadBytes {
		// It can have overshot when ContentLength was -1 (chunked) — delete the file
		// and return an error instead of leaving partial garbage behind.
		_ = os.Remove(full)
		writeErr(w, http.StatusRequestEntityTooLarge, "download exceeded the cap mid-stream")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saved": full, "size": n})
}

// ─── HASH ──────────────────────────────────────────────────────

func handleHash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	p := r.URL.Query().Get("path")
	algo := strings.ToLower(r.URL.Query().Get("algo"))
	if algo == "" {
		algo = "sha256"
	}
	if err := validatePath(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p = filepath.Clean(p)
	var h hash.Hash
	switch algo {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	case "sha256":
		h = sha256.New()
	default:
		writeErr(w, http.StatusBadRequest, "invalid algo (md5|sha1|sha256)")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"algo": algo, "hash": hex.EncodeToString(h.Sum(nil))})
}

// ─── TOUCH (create empty file) ─────────────────────────────────

func handleTouch(w http.ResponseWriter, r *http.Request) {
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
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f.Close()
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
