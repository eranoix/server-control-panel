package files

// This file exists separately from files.go because the Android app needs
// JSON-native, mtime-aware primitives (to detect a concurrent-edit conflict),
// while the desktop panel (files.go) goes on serving plain text without that
// check — none of the existing behavior changes.
// MobileList/MobileRead/MobileWrite reuse validatePath (the same denylist as
// the panel) and never duplicate the path sanitization logic.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// MobileListResult is the response of MobileList. Entries reuses the `entry`
// type from files.go (same package) instead of redefining the same fields.
type MobileListResult struct {
	Path    string  `json:"path"`
	Parent  string  `json:"parent"`
	Entries []entry `json:"entries"`
}

// MobileReadResult is the response of MobileRead.
type MobileReadResult struct {
	Content  string `json:"content"`
	Mtime    int64  `json:"mtime"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
}

// Sentinels comparable through errors.Is — internal/mobilebff maps each one
// to the right HTTP status without this package needing to know any HTTP.
var (
	ErrBinary   = errors.New("binary file")
	ErrTooLarge = errors.New("file too large")
	ErrConflict = errors.New("file changed since last read")
)

// maxMobileReadSize is the same ceiling as handleRead (files.go) — the phone
// editor has no reason to open anything bigger than that, and pulling more
// than that into memory to hand back as JSON would be a cheap denial of
// service (a single GET forces the server to allocate the entire file).
const maxMobileReadSize = 2 * 1024 * 1024

// mobileLanguageByExt derives the language hint from the extension alone (no
// content sniffing) so the app can pick the syntax highlighting grammar
// (sora-editor / TextMate).
var mobileLanguageByExt = map[string]string{
	".go":   "go",
	".py":   "python",
	".js":   "javascript",
	".ts":   "javascript",
	".json": "json",
	".yaml": "yaml",
	".yml":  "yaml",
	".sh":   "shell",
	".md":   "markdown",
}

func languageFor(path string) string {
	if lang, ok := mobileLanguageByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return lang
	}
	return "plaintext"
}

// resolveReal validates the raw path with validatePath (the denylist from
// files.go), resolves symlinks down to the real absolute path, and runs
// validatePath AGAIN over the resolved path.
//
// Why the second check: validatePath only ever sees the string it was given.
// A symlink inside an allowed directory, pointing at /etc/shadow or into
// /opt/panel/data/secrets, would sail through the first check (the
// link's own string does not match the denylist) and would only reveal its
// true target at os.Open/os.ReadFile/os.WriteFile time — when it is already
// too late. Resolving first and validating again closes that detour without
// duplicating the deny list: it is the same validatePath, called twice.
func resolveReal(p string) (string, error) {
	if err := validatePath(p); err != nil {
		return "", err
	}
	cleaned := filepath.Clean(p)
	real, err := realpathAllowMissing(cleaned)
	if err != nil {
		return "", err
	}
	if err := validatePath(real); err != nil {
		return "", fmt.Errorf("resolved path is on the deny list: %w", err)
	}
	return real, nil
}

// realpathAllowMissing resolves symlinks in `cleaned`, tolerating that the
// path itself (or part of it) may not exist yet — MobileWrite has to work
// for creating a brand new file, including inside directories that do not
// exist yet (the same support handleWrite already gives via
// os.MkdirAll(filepath.Dir(p), ...)). It walks up until it finds the deepest
// ancestor that already exists, resolves that ancestor, and puts the
// still-missing suffix back on top (there is nothing in it to resolve).
func realpathAllowMissing(cleaned string) (string, error) {
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		return real, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(cleaned)
	if dir == cleaned {
		// Reached the filesystem root without finding an existing ancestor.
		return cleaned, nil
	}
	realDir, err := realpathAllowMissing(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(realDir, filepath.Base(cleaned)), nil
}

// MobileList lists the contents of a directory in the same format and
// ordering as handleList (files.go), but handed back as a Go value instead of
// written straight into an http.ResponseWriter — so that internal/mobilebff
// can shape the app's JSON response without re-implementing the directory read.
func MobileList(path string) (MobileListResult, error) {
	real, err := resolveReal(path)
	if err != nil {
		return MobileListResult{}, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return MobileListResult{}, err
	}
	if !fi.IsDir() {
		return MobileListResult{}, fmt.Errorf("not a directory")
	}
	dirEntries, err := os.ReadDir(real)
	if err != nil {
		return MobileListResult{}, err
	}
	out := make([]entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		full := filepath.Join(real, de.Name())
		lfi, err := os.Lstat(full)
		if err != nil {
			continue
		}
		e := entry{
			Name:     de.Name(),
			Size:     lfi.Size(),
			Mode:     lfi.Mode().String(),
			Modified: lfi.ModTime().Unix(),
			IsDir:    lfi.IsDir(),
			IsLink:   lfi.Mode()&os.ModeSymlink != 0,
		}
		if e.IsLink {
			if tgt, err := os.Readlink(full); err == nil {
				abs := tgt
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(real, abs)
				}
				abs = filepath.Clean(abs)
				e.Target = tgt
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
	return MobileListResult{Path: real, Parent: filepath.Dir(real), Entries: out}, nil
}

// MobileRead reads a text file and returns content + mtime + language hint.
// It rejects directories, binary files (a null-byte scan over the content
// already loaded — the whole file fits in memory because it is capped at
// maxMobileReadSize before ever being read) and files larger than the
// ceiling.
func MobileRead(path string) (MobileReadResult, error) {
	real, err := resolveReal(path)
	if err != nil {
		return MobileReadResult{}, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return MobileReadResult{}, err
	}
	if fi.IsDir() {
		return MobileReadResult{}, fmt.Errorf("is a directory")
	}
	if fi.Size() > maxMobileReadSize {
		return MobileReadResult{}, ErrTooLarge
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return MobileReadResult{}, err
	}
	for _, c := range b {
		if c == 0 {
			return MobileReadResult{}, ErrBinary
		}
	}
	return MobileReadResult{
		Content:  string(b),
		Mtime:    fi.ModTime().Unix(),
		Language: languageFor(real),
		Size:     fi.Size(),
	}, nil
}

// writeLocks serializes MobileWrite per path (not globally) — concurrent
// writes to different files never contend for the same mutex.
var writeLocks sync.Map // map[string]*sync.Mutex

func lockFor(path string) *sync.Mutex {
	v, _ := writeLocks.LoadOrStore(path, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// MobileWrite writes the content to `path`, with an mtime-based conflict
// check: if `expectedMtime` is zero, the caller has no prior read (e.g.
// creating a brand new file) and the write is unconditional; otherwise the
// file's current mtime on disk has to match `expectedMtime` — if it does not,
// it returns ErrConflict without writing anything (it does not even touch the
// file). If the expected file no longer exists, that counts as a conflict too
// (something changed the state the caller last read).
//
// The write itself is atomic (temporary file in the same directory + rename,
// the same pattern as internal/auth/trusted_devices.go) so that a crash
// mid-write never leaves the file corrupted/truncated.
//
// It returns the file's new mtime (unix) after the write, so that the HTTP
// handler does not have to re-read the entire content just to answer with the
// updated mtime.
func MobileWrite(path string, content string, expectedMtime int64) (int64, error) {
	real, err := resolveReal(path)
	if err != nil {
		return 0, err
	}

	mu := lockFor(real)
	mu.Lock()
	defer mu.Unlock()

	if expectedMtime != 0 {
		fi, err := os.Stat(real)
		if err != nil {
			if os.IsNotExist(err) {
				return 0, ErrConflict
			}
			return 0, err
		}
		if fi.ModTime().Unix() != expectedMtime {
			return 0, ErrConflict
		}
	}

	if err := os.MkdirAll(filepath.Dir(real), 0755); err != nil {
		return 0, err
	}
	tmp := real + ".mobile-tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, real); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}

	fi, err := os.Stat(real)
	if err != nil {
		return 0, err
	}
	return fi.ModTime().Unix(), nil
}
