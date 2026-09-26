package api

// handlers_fdroid.go — serves the self-hosted F-Droid repository directly
// from disk, byte-for-byte, with zero redirects (PITFALLS.md Pitfall 14):
// the F-Droid client does not follow 3xx responses, so a redirect anywhere
// on the install/update path fails silently on-device with no server-side
// signal. Every response here goes through http.ServeContent, which gives
// Range + conditional-request handling for free — required so a paused APK
// download resumes correctly.
//
// This route is deliberately NOT registered on r.mux the way every other
// public route in this file is (see NewRouter, "Public" section). Go's
// http.ServeMux redirects (301) any request whose path differs from its
// filepath.Clean()'d form — a doubled slash, a "." or ".." segment — BEFORE
// it ever reaches a registered handler, regardless of whether a pattern
// matches. That is exactly the kind of redirect this route exists to rule
// out, and it fires for both legitimate path variants and traversal
// attempts alike. fdroidGate intercepts requests under the prefix earlier
// in the middleware chain, ahead of r.mux, so ServeMux's own path-cleaning
// never runs for this route; every other route's behavior is unaffected.
//
// Path validation below mirrors the defense-in-depth shape of
// internal/files/files.go's validatePath: clean, reject any ".." segment,
// and confirm the resolved path is still inside the configured repo root
// before ever calling os.Open.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// fdroidRepoPrefix is the public, unauthenticated mount point for the
// self-hosted F-Droid repository. Data lives under
// <cfg.DataDir>/fdroid/repo — see docs/android-fdroid-repo.md.
const fdroidRepoPrefix = "/fdroid/repo/"

// fdroidGate intercepts requests under fdroidRepoPrefix before they reach
// r.mux, so Go's ServeMux never gets a chance to 301 a "." /".."/doubled-
// slash path on this route. Every other path falls through to next
// unchanged.
func (r *Router) fdroidGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, fdroidRepoPrefix) {
			r.handleFdroidRepo(w, req)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// fdroidContentType maps a served file's extension to the Content-Type the
// F-Droid client (and the icons the panel itself may preview) expects.
// Deliberately not delegated to mime.TypeByExtension: that function's
// result depends on the host's /etc/mime.types, which is not guaranteed to
// register .apk/.jar consistently across distros — this route needs a
// stable, deterministic answer.
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

// handleFdroidRepo serves <cfg.DataDir>/fdroid/repo/* directly. Public,
// unauthenticated by design — the F-Droid client presents no vps-manager
// session cookie and never will. Reached only via fdroidGate, never
// registered on r.mux.
func (r *Router) handleFdroidRepo(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rel := strings.TrimPrefix(req.URL.Path, fdroidRepoPrefix)

	// Anchor at "/" before Clean so a leading run of ".." can never walk
	// past the root the way it could from a relative string.
	cleaned := filepath.Clean("/" + rel)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			writeErr(w, http.StatusBadRequest, "invalid path")
			return
		}
	}
	if cleaned == "/" {
		// Bare directory request (repo root itself). Never list, never
		// redirect to a normalized form — exactly what http.FileServer
		// would do here and Pitfall 14 forbids.
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
		// A subdirectory request (e.g. icons/): 404, never a listing, never
		// a redirect to add/remove a trailing slash.
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	w.Header().Set("Content-Type", fdroidContentType(full))
	http.ServeContent(w, req, filepath.Base(full), info.ModTime(), f)
}
