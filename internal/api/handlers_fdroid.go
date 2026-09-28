package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const fdroidRepoPrefix = "/fdroid/repo/"

func (r *Router) fdroidGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, fdroidRepoPrefix) {
			r.handleFdroidRepo(w, req)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func fdroidContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".apk":
		return "application/vnd.android.package-archive"
	case ".jar":
		return "application/java-archive"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

func (r *Router) handleFdroidRepo(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rel := strings.TrimPrefix(req.URL.Path, fdroidRepoPrefix)

	cleaned := filepath.Clean("/" + rel)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			writeErr(w, http.StatusBadRequest, "invalid path")
			return
		}
	}
	if cleaned == "/" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	root := filepath.Join(r.cfg.DataDir, "fdroid", "repo")
	full := filepath.Join(root, cleaned)
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	f, err := os.Open(full)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	w.Header().Set("Content-Type", fdroidContentType(full))
	http.ServeContent(w, req, filepath.Base(full), info.ModTime(), f)
}
