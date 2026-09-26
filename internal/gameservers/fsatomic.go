package gameservers

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The package's single file-writing path.
//
// Why owner and mode are part of the contract and not a detail: the panel runs
// as root and the game container runs as the game's user (4711 on Enshrouded,
// 1000 on Palworld). The container's start.sh checks whether it can write to
// the file and ABORTS the boot if it cannot. A config rewritten as root:root
// breaks nothing right away — it breaks on the next restart, far from the
// action that caused it. The previous idiom (WriteFile + Chmod + Rename)
// preserved the mode and silently lost the owner.

// escreveAtomico replaces the content of `path` preserving OWNER and MODE.
//
// The sequence, and why each step exists:
//
//  1. a uniquely named temporary in the SAME directory (os.CreateTemp). Same
//     directory because Rename is only atomic within one filesystem; unique name
//     because `path + ".tmp"` collides between two concurrent writers and can be
//     pre-created by a third party.
//  2. Chmod and Chown through the DESCRIPTOR (*os.File), not the path functions:
//     that closes the TOCTOU window between creating the temporary and adjusting it.
//  3. owner and mode come from the `stat` of the file ALREADY on disk. When the
//     file does not exist yet, they come from `ref` (the file or the server root).
//     Never from a constant — a constant is how the defect returns under another name.
//  4. f.Sync() before the Rename and Sync() of the directory after. rename(2) is
//     atomic for concurrent observers, but is NOT durable until the ZFS
//     transaction group closes (measured on the host: zfs_txg_timeout = 5), and
//     this house has a UPS with no data cable. A cut inside that window would
//     leave the UI saying "saved" and the server reading the old file on next boot.
func escreveAtomico(path string, conteudo []byte, ref string) error {
	uid, gid, modo, err := donoEModoDestino(path, ref)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("could not create a temporary file in %s: %w", dir, err)
	}
	tmp := f.Name()
	fechado := false
	// Any exit through an error takes the temporary with it: predictable litter
	// beside a config is noise the next session reads as a valid file.
	defer func() {
		if !fechado {
			_ = f.Close()
		}
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()

	if _, err := f.Write(conteudo); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Chmod(modo); err != nil {
		return fmt.Errorf("chmod %04o on %s: %w", modo, tmp, err)
	}
	if err := chownDescritor(f, uid, gid); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync of %s: %w", tmp, err)
	}
	fechado = true
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	tmp = "" // renamed: there is nothing left to remove

	return sincronizaDir(dir)
}

// chownDescritor sets the owner through the open descriptor.
//
// The failure is only tolerated when the temporary was ALREADY born with the
// wanted owner (the case of running the panel as the game's own user). Swallowing
// the failure in any other case would reintroduce the defect: the file would end
// up with the wrong owner and nobody would know until the container failed to come up.
func chownDescritor(f *os.File, uid, gid int) error {
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

// sincronizaDir makes sure the directory entry created by the Rename is on
// disk, not only in cache. Without it the content is durable but the name is not.
func sincronizaDir(dir string) error {
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

// donoEModoDestino decides which owner and mode the file must end up with.
//
// Order: the file itself, if it exists (preserving is always the right answer);
// otherwise `ref`. If `ref` is a directory, the mode loses the execute bits — a
// config is not executable just because its folder is.
func donoEModoDestino(path, ref string) (uid, gid int, modo os.FileMode, err error) {
	if fi, e := os.Stat(path); e == nil && fi.Mode().IsRegular() {
		u, g, e2 := donoDe(path)
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
	u, g, e2 := donoDe(ref)
	if e2 != nil {
		return 0, 0, 0, e2
	}
	m := fi.Mode().Perm()
	if fi.IsDir() {
		m &^= 0o111
	}
	return u, g, m, nil
}

// donoDe reads uid/gid from a path. It is the package's ONLY source of uid.
func donoDe(p string) (int, int, error) {
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

// chownComoRef applies to `alvo` the owner observed on `ref`.
//
// It replaces the literal pair 4711, 4711: the right value is the one on disk. If
// `ref` cannot be inspected, the error names the path — it never falls back to a
// default, because a default is a new constant under another name.
func chownComoRef(alvo, ref string, recursivo bool) error {
	uid, gid, err := donoDe(ref)
	if err != nil {
		return fmt.Errorf("reference owner unavailable to adjust %s: %w", alvo, err)
	}
	if recursivo {
		return chownTree(alvo, uid, gid)
	}
	if err := os.Chown(alvo, uid, gid); err != nil {
		return fmt.Errorf("chown of %s to %d:%d: %w", alvo, uid, gid, err)
	}
	return nil
}
