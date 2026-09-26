package webassets

import (
	"net/http"
	"strings"
)

// HandleIcon serves the pre-rendered logo PNGs (web/*.png). Before: it drew a
// procedural "VM" out of rectangles. Now: a dispatch to a static file
// (rsvg-convert on logo-mark.svg generated the 7 sizes once).
// Changing the logo = re-rendering the PNGs, without touching Go.
func HandleIcon(w http.ResponseWriter, req *http.Request) {
	file := "web/icon-192.png"
	switch {
	case strings.HasSuffix(req.URL.Path, "/icon-512-maskable.png"):
		file = "web/icon-512-maskable.png"
	case strings.HasSuffix(req.URL.Path, "/icon-512.png"):
		file = "web/icon-512.png"
	case strings.HasSuffix(req.URL.Path, "/apple-touch-icon-152.png"):
		file = "web/apple-touch-icon-152.png"
	case strings.HasSuffix(req.URL.Path, "/apple-touch-icon-167.png"):
		file = "web/apple-touch-icon-167.png"
	case strings.HasSuffix(req.URL.Path, "/apple-touch-icon-precomposed.png"):
		file = "web/apple-touch-icon-precomposed.png"
	case strings.HasSuffix(req.URL.Path, "/apple-touch-icon.png"):
		file = "web/apple-touch-icon.png"
	case strings.HasSuffix(req.URL.Path, "/icon-192.png"):
		file = "web/icon-192.png"
	}
	data, err := FS.ReadFile(file)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
