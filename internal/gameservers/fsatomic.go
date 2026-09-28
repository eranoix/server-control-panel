package gameservers

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func writeAtomic(path string, content []byte, ref string) error {
	uid, gid, mode, err := destOwnerAndMode(path, ref)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("could not create a temporary file in %s: %w", dir, err)
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
		return fmt.Errorf("chmod %04o on %s: %w", mode, tmp, err)
	}
	if err := chownFD(f, uid, gid); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync of %s: %w", tmp, err)
	}
	closed = true
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	tmp = ""

	return syncDir(dir)
}

func chownFD(f *os.File, uid, gid int) error {
	if err := f.Chown(uid, gid); err != nil {
		if fi, serr := f.Stat(); serr == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid && int(st.Gid) == gid {
				return nil
			}
		}
		return fmt.Errorf("could not set %s to %d:%d — the game container would not be able to write to it: %w", f.Name(), uid, gid, err)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s for fsync: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync of %s: %w", dir, err)
	}
	return nil
}

func destOwnerAndMode(path, ref string) (uid, gid int, mode os.FileMode, err error) {
	if fi, e := os.Stat(path); e == nil && fi.Mode().IsRegular() {
		u, g, e2 := ownerOf(path)
		if e2 != nil {
			return 0, 0, 0, e2
		}
		return u, g, fi.Mode().Perm(), nil
	}
	if ref == "" {
		return 0, 0, 0, fmt.Errorf("%s does not exist yet and no owner reference was given — writing like this would create a root:root file in the middle of the game tree", path)
	}
	fi, e := os.Stat(ref)
	if e != nil {
		return 0, 0, 0, fmt.Errorf("could not inspect the owner reference %s (target %s): %w", ref, path, e)
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
		return 0, 0, fmt.Errorf("could not read the owner of %s: %w", p, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("could not read the owner of %s: stat without Stat_t", p)
	}
	return int(st.Uid), int(st.Gid), nil
}

func chownLikeRef(target, ref string, recursive bool) error {
	uid, gid, err := ownerOf(ref)
	if err != nil {
		return fmt.Errorf("reference owner unavailable to adjust %s: %w", target, err)
	}
	if recursive {
		return chownTree(target, uid, gid)
	}
	if err := os.Chown(target, uid, gid); err != nil {
		return fmt.Errorf("chown of %s to %d:%d: %w", target, uid, gid, err)
	}
	return nil
}
