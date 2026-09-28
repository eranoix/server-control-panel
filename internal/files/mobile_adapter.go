package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type MobileListResult struct {
	Path    string  `json:"path"`
	Parent  string  `json:"parent"`
	Entries []entry `json:"entries"`
}

type MobileReadResult struct {
	Content  string `json:"content"`
	Mtime    int64  `json:"mtime"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
}

var (
	ErrBinary   = errors.New("binary file")
	ErrTooLarge = errors.New("file too large")
	ErrConflict = errors.New("file changed since last read")
)

const maxMobileReadSize = 2 * 1024 * 1024

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

func realpathAllowMissing(cleaned string) (string, error) {
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		return real, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(cleaned)
	if dir == cleaned {
		return cleaned, nil
	}
	realDir, err := realpathAllowMissing(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(realDir, filepath.Base(cleaned)), nil
}

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
					e.Target = tgt + " (protected)"
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

var writeLocks sync.Map

func lockFor(path string) *sync.Mutex {
	v, _ := writeLocks.LoadOrStore(path, &sync.Mutex{})
	return v.(*sync.Mutex)
}

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
