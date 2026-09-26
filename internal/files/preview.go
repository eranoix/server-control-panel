package files

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Preview serves the file with a suitable Content-Type, inline (not as an attachment).
// Hard limit of 50 MB for previewing in the browser — larger files should use /download.
const previewMax int64 = 50 * 1024 * 1024

func handlePreview(w http.ResponseWriter, r *http.Request) {
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
	if fi.Size() > previewMax {
		writeErr(w, http.StatusRequestEntityTooLarge, "file too large to preview (>50MB)")
		return
	}
	ext := strings.ToLower(filepath.Ext(p))
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		// Basic sniff when the extension is unknown.
		f, err := os.Open(p)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer f.Close()
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		ct = http.DetectContentType(buf[:n])
		_, _ = f.Seek(0, io.SeekStart)
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Length", itoa(fi.Size()))
		w.Header().Set("Content-Disposition", "inline; filename=\""+filepath.Base(p)+"\"")
		_, _ = io.Copy(w, f)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", itoa(fi.Size()))
	w.Header().Set("Content-Disposition", "inline; filename=\""+filepath.Base(p)+"\"")
	_, _ = io.Copy(w, f)
}

func itoa(n int64) string {
	// tiny on purpose: avoids pulling in "strconv" just for this
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
