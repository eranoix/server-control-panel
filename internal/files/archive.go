package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ─── COMPRESS ───────────────────────────────────────────────────

type archiveReq struct {
	Paths []string `json:"paths"`          // files/folders to compress
	Dest  string   `json:"dest"`           // path of the .zip/.tar.gz/.7z
	Base  string   `json:"base,omitempty"` // base directory (entries inside the archive are relative to it). Default: parent folder of the paths.
}

func decodeArchive(r *http.Request) (*archiveReq, error) {
	var req archiveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	if len(req.Paths) == 0 {
		return nil, fmt.Errorf("paths required")
	}
	if err := validatePath(req.Dest); err != nil {
		return nil, fmt.Errorf("dest: %w", err)
	}
	req.Dest = filepath.Clean(req.Dest)
	for i, p := range req.Paths {
		if err := validatePath(p); err != nil {
			return nil, fmt.Errorf("paths[%d]: %w", i, err)
		}
		req.Paths[i] = filepath.Clean(p)
	}
	if req.Base != "" {
		if err := validatePath(req.Base); err != nil {
			return nil, fmt.Errorf("base: %w", err)
		}
		req.Base = filepath.Clean(req.Base)
	} else {
		req.Base = filepath.Dir(req.Paths[0])
	}
	return &req, nil
}

func relName(base, path string) (string, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", err
	}
	// Normalize the separator to "/" (the neutral form inside the archive).
	return filepath.ToSlash(rel), nil
}

// Iterates each requested path; if it is a dir, walks it recursively. Calls fn(absPath, rel, info) per entry.
func walkPaths(base string, paths []string, fn func(abs, rel string, info os.FileInfo) error) error {
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, err := relName(base, p)
		if err != nil {
			return err
		}
		if err := fn(p, rel, info); err != nil {
			return err
		}
		if info.IsDir() {
			err := filepath.Walk(p, func(sub string, fi os.FileInfo, werr error) error {
				if werr != nil {
					return werr
				}
				if sub == p {
					return nil
				}
				r, err := relName(base, sub)
				if err != nil {
					return err
				}
				return fn(sub, r, fi)
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func handleZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	req, err := decodeArchive(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.HasSuffix(strings.ToLower(req.Dest), ".zip") {
		req.Dest += ".zip"
	}
	f, err := os.Create(req.Dest)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	err = walkPaths(req.Base, req.Paths, func(abs, rel string, info os.FileInfo) error {
		if rel == "" || rel == "." {
			return nil
		}
		if info.IsDir() {
			_, e := zw.Create(rel + "/")
			return e
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate
		wfh, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(wfh, src)
		return err
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dest": req.Dest})
}

func handleTar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	req, err := decodeArchive(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	compress := strings.HasSuffix(strings.ToLower(req.Dest), ".tar.gz") || strings.HasSuffix(strings.ToLower(req.Dest), ".tgz")
	if !compress && !strings.HasSuffix(strings.ToLower(req.Dest), ".tar") {
		req.Dest += ".tar.gz"
		compress = true
	}
	f, err := os.Create(req.Dest)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	var out io.WriteCloser = f
	if compress {
		gz := gzip.NewWriter(f)
		defer gz.Close()
		out = gz
	}
	tw := tar.NewWriter(out)
	defer tw.Close()
	err = walkPaths(req.Base, req.Paths, func(abs, rel string, info os.FileInfo) error {
		if rel == "" || rel == "." {
			return nil
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(abs)
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() || link != "" {
			return nil
		}
		src, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dest": req.Dest})
}

// 7z shells out to the external `7z` binary (p7zip-full). No Go library writes 7z.
func handle7z(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	req, err := decodeArchive(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.HasSuffix(strings.ToLower(req.Dest), ".7z") {
		req.Dest += ".7z"
	}
	// Remove an existing dest so that 7z does not stop to ask "Overwrite?".
	_ = os.Remove(req.Dest)
	args := []string{"a", "-t7z", "-mx=5", req.Dest}
	args = append(args, req.Paths...)
	// 10min timeout — large archives can legitimately take a while, but a
	// corrupt binary or an endless path must not hang a request forever.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "7z", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("7z: %v — %s", err, strings.TrimSpace(string(out))))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dest": req.Dest})
}

// ─── EXTRACT ───────────────────────────────────────────────────

type extractReq struct {
	Archive string `json:"archive"`
	Dest    string `json:"dest"`
}

func handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusBadRequest, "method not allowed")
		return
	}
	var req extractReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePath(req.Archive); err != nil {
		writeErr(w, http.StatusBadRequest, "archive: "+err.Error())
		return
	}
	if err := validatePath(req.Dest); err != nil {
		writeErr(w, http.StatusBadRequest, "dest: "+err.Error())
		return
	}
	arch := filepath.Clean(req.Archive)
	dest := filepath.Clean(req.Dest)
	if err := os.MkdirAll(dest, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	low := strings.ToLower(arch)
	switch {
	case strings.HasSuffix(low, ".zip"):
		if err := extractZip(arch, dest); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz") || strings.HasSuffix(low, ".tar"):
		if err := extractTar(arch, dest); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case strings.HasSuffix(low, ".7z"):
		// 7z x -o<dest> <arch>  (extracts preserving paths) with a timeout
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "7z", "x", "-y", "-o"+dest, arch)
		if out, err := cmd.CombinedOutput(); err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Sprintf("7z: %v — %s", err, strings.TrimSpace(string(out))))
			return
		}
		// 7z is an external binary — there is no per-entry hook to block escaping
		// symlinks. Sweep the result afterwards and remove them.
		if err := validateNoEscapingSymlinks(dest); err != nil {
			writeErr(w, http.StatusInternalServerError, "archive contains symlinks outside the destination: "+err.Error())
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "unsupported file type (use .zip / .tar.gz / .tgz / .tar / .7z)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dest": dest})
}

// Anti zip-slip: makes sure the final path stays inside dest.
func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry escapes dest: %s", name)
	}
	target := filepath.Join(dest, clean)
	rel, err := filepath.Rel(dest, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("entry escapes dest: %s", name)
	}
	return target, nil
}

// symlinkEscapes reports whether a symlink with `linkname` placed at
// `linkPath` would point outside the `dest` root. linkname may be absolute
// ("/etc/passwd") or relative ("../../../etc/passwd").
func symlinkEscapes(dest, linkPath, linkname string) bool {
	var resolved string
	if filepath.IsAbs(linkname) {
		resolved = filepath.Clean(linkname)
	} else {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(linkPath), linkname))
	}
	rel, err := filepath.Rel(dest, resolved)
	if err != nil {
		return true
	}
	return strings.HasPrefix(rel, "..")
}

// validateNoEscapingSymlinks walks dest recursively and removes any symlink
// whose target escapes dest. Used after extraction through 7z (an external
// binary) where there is no per-entry control.
func validateNoEscapingSymlinks(dest string) error {
	return filepath.Walk(dest, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		linkname, err := os.Readlink(p)
		if err != nil {
			return err
		}
		if symlinkEscapes(dest, p, linkname) {
			_ = os.Remove(p)
			return fmt.Errorf("symlink escaped dest and was removed: %s -> %s", p, linkname)
		}
		return nil
	})
}

func extractZip(arch, dest string) error {
	zr, err := zip.OpenReader(arch)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, ze := range zr.File {
		target, err := safeJoin(dest, ze.Name)
		if err != nil {
			return err
		}
		if ze.FileInfo().IsDir() {
			if err := os.MkdirAll(target, ze.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		src, err := ze.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, ze.Mode())
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTar(arch, dest string) error {
	f, err := os.Open(arch)
	if err != nil {
		return err
	}
	defer f.Close()
	var rdr io.Reader = f
	if strings.HasSuffix(strings.ToLower(arch), ".gz") || strings.HasSuffix(strings.ToLower(arch), ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		rdr = gz
	}
	tr := tar.NewReader(rdr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink, tar.TypeLink:
			// Symlinks/hardlinks are the classic tar-slip vector — header.Linkname can
			// be "/etc/passwd" or "../../../etc/passwd". Validate before creating; the
			// escaping ones are dropped silently rather than aborting the whole
			// extraction.
			if symlinkEscapes(dest, target, hdr.Linkname) {
				continue
			}
			if hdr.Typeflag == tar.TypeLink {
				linkTarget, err := safeJoin(dest, hdr.Linkname)
				if err != nil {
					continue
				}
				_ = os.Link(linkTarget, target)
			} else {
				_ = os.Symlink(hdr.Linkname, target)
			}
		}
	}
	return nil
}
