package singbox

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func nowUnix() int64 { return time.Now().Unix() }

// writeAtomic replaces path preserving owner+mode, mirroring
// internal/gameservers.escreveAtomico: unique temp in the same dir → chmod/chown
// by descriptor → fsync → rename → dir fsync. The container reading
// /opt/singbox/config.json runs as root; owner preservation keeps a future
// non-root setup from silently breaking on the next restart. ref supplies
// owner/mode when path does not exist yet.
func writeAtomic(path string, content []byte, ref string) error {
	uid, gid, mode, err := ownerMode(path, ref)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("temporary file in %s: %w", dir, err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := f.Chown(uid, gid); err != nil {
		// tolerate only if it already has the desired owner
		if fi, serr := f.Stat(); serr == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid || int(st.Gid) != gid {
				return fmt.Errorf("chown %s → %d:%d: %w", tmp, uid, gid, err)
			}
		} else {
			return fmt.Errorf("chown %s → %d:%d: %w", tmp, uid, gid, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	closed = true
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	tmp = ""
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s for fsync: %w", dir, err)
	}
	defer d.Close()
	return d.Sync()
}

// ownerMode returns the owner+mode the file should end with: the existing
// file's if present, else ref's (regular file or dir).
func ownerMode(path, ref string) (uid, gid int, mode os.FileMode, err error) {
	if fi, e := os.Stat(path); e == nil && fi.Mode().IsRegular() {
		u, g, e2 := ownerOf(path)
		if e2 != nil {
			return 0, 0, 0, e2
		}
		return u, g, fi.Mode().Perm(), nil
	}
	if ref == "" {
		return 0, 0, 0o600, nil
	}
	fi, e := os.Stat(ref)
	if e != nil {
		return 0, 0, 0o600, nil
	}
	u, g, e2 := ownerOf(ref)
	if e2 != nil {
		return 0, 0, 0, e2
	}
	m := fi.Mode().Perm()
	if fi.IsDir() {
		m &^= 0o111
	}
	return u, g, m, nil
}

func ownerOf(p string) (int, int, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("stat without Stat_t: %s", p)
	}
	return int(st.Uid), int(st.Gid), nil
}
