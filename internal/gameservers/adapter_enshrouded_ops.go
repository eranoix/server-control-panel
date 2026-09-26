package gameservers

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// World and backup operations of the Enshrouded adapter.
//
// Everything here touches a save — the rule is: NEVER write to the savegame with
// the server up. The handlers stop the container before calling Restore.

// safeName rejects a name that could escape the worlds directory.
func safeName(n string) error {
	if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
		return fmt.Errorf("invalid name: %q", n)
	}
	return nil
}

// DuplicateWorld copies a world's whole family of files, preserving the
// .saveid — two worlds may share the same save id without conflict, because
// only one at a time goes to savegame (renamed to the canonical id).
func (e enshrouded) DuplicateWorld(s Server, src, dst string) error {
	if err := safeName(src); err != nil {
		return err
	}
	if err := safeName(dst); err != nil {
		return err
	}
	from := filepath.Join(s.Root, "worlds", src)
	to := filepath.Join(s.Root, "worlds", dst)
	if _, err := os.Stat(from); err != nil {
		return fmt.Errorf("world '%s' does not exist", src)
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("a world named '%s' already exists", dst)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(from, en.Name()), filepath.Join(to, en.Name())); err != nil {
			os.RemoveAll(to) // do not leave a half-finished copy
			return err
		}
	}
	return nil
}

// DeleteWorld removes an archived world. It refuses to delete the active one —
// the active one lives in savegame and deleting it here would leave the server
// with no world to archive.
func (e enshrouded) DeleteWorld(s Server, name string) error {
	if err := safeName(name); err != nil {
		return err
	}
	if e.ActiveWorld(s) == name {
		return fmt.Errorf("'%s' is active — switch worlds before deleting", name)
	}
	dir := filepath.Join(s.Root, "worlds", name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("world '%s' does not exist", name)
	}
	return os.RemoveAll(dir)
}

func enshBackupDir(s Server) string {
	return filepath.Join(s.Root, "data", "server", "backups")
}
func enshSaveDir(s Server) string {
	return filepath.Join(s.Root, "data", "server", "savegame")
}

// BackupPath resolves a backup's path validating the name, so a download does
// not turn into arbitrary file reading.
func (enshrouded) BackupPath(s Server, file string) (string, error) {
	if err := safeName(file); err != nil {
		return "", err
	}
	p := filepath.Join(enshBackupDir(s), file)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("backup '%s' does not exist", file)
	}
	return p, nil
}

// CreateBackup zips the current savegame. The name carries the active world so
// you can tell what is inside without opening it.
func (e enshrouded) CreateBackup(s Server, stamp string) (string, error) {
	src := enshSaveDir(s)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("savegame not found")
	}
	if err := os.MkdirAll(enshBackupDir(s), 0o755); err != nil {
		return "", err
	}
	world := e.ActiveWorld(s)
	if world == "" {
		world = "savegame"
	}
	name := fmt.Sprintf("manual-%s-%s.zip", world, stamp)
	out := filepath.Join(enshBackupDir(s), name)

	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	entries, err := os.ReadDir(src)
	if err != nil {
		zw.Close()
		os.Remove(out)
		return "", err
	}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		if err := zipAdd(zw, filepath.Join(src, en.Name()), en.Name()); err != nil {
			zw.Close()
			os.Remove(out)
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		os.Remove(out)
		return "", err
	}
	return name, nil
}

// RestoreBackup overwrites the savegame with the zip's content.
//
// The caller MUST have stopped the server beforehand. Before touching anything,
// it takes a safety zip of the current state — restoring the wrong backup is
// irreversible otherwise.
func (e enshrouded) RestoreBackup(s Server, file, stamp string) error {
	path, err := e.BackupPath(s, file)
	if err != nil {
		return err
	}
	if _, err := e.CreateBackup(s, "pre-restore-"+stamp); err != nil {
		return fmt.Errorf("could not save the current state before restoring: %w", err)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("invalid zip: %w", err)
	}
	defer zr.Close()

	save := enshSaveDir(s)
	if err := os.MkdirAll(save, 0o755); err != nil {
		return err
	}
	// Clean only loose savegame files (the backup brings the whole family).
	olds, _ := os.ReadDir(save)
	for _, o := range olds {
		if !o.IsDir() {
			_ = os.Remove(filepath.Join(save, o.Name()))
		}
	}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		// Zip Slip: never trust the name inside the zip.
		name := filepath.Base(zf.Name)
		if err := safeName(name); err != nil {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(filepath.Join(save, name))
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(dst, rc)
		dst.Close()
		rc.Close()
		if cerr != nil {
			return cerr
		}
	}
	// The owner of the restored save comes from OBSERVING the disk, never from a
	// constant. The literal pair that used to be here is Enshrouded's uid; Palworld
	// uses PUID 1000 — assuming the panel knows the game's uid is how this defect
	// was born the first time.
	//
	// The reference is s.Root, and not `save`'s immediate parent: the parent may
	// have just been created by the panel, already root:root. The server's root is
	// the only point of the tree whose owner is reliably the game's. An inspectable
	// root is a precondition — without it, an error naming the path, never an invented default.
	return chownComoRef(save, s.Root, true)
}

// ConnectionInfo exposes what a player needs in order to join (the password
// comes from the server config, shown masked in the UI).
func (enshrouded) ConnectionInfo(s Server) map[string]interface{} {
	out := map[string]interface{}{"address": s.Address}
	cfg, err := readJSONFile(enshConfigPath(s))
	if err != nil {
		return out
	}
	if groups, ok := cfg["userGroups"].([]interface{}); ok && len(groups) > 0 {
		if g0, ok := groups[0].(map[string]interface{}); ok {
			out["password"] = g0["password"]
		}
	}
	out["slotCount"] = cfg["slotCount"]
	out["serverName"] = cfg["name"]
	return out
}

// ---------- helpers ----------

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if fi, err := os.Stat(src); err == nil {
		_ = os.Chmod(dst, fi.Mode())
	}
	return nil
}

func zipAdd(zw *zip.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func chownTree(dir string, uid, gid int) error {
	return filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Chown(p, uid, gid)
		return nil
	})
}
