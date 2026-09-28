package gameservers

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func safeName(n string) error {
	if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
		return fmt.Errorf("invalid name: %q", n)
	}
	return nil
}

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
			os.RemoveAll(to)
			return err
		}
	}
	return nil
}

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
	return chownLikeRef(save, s.Root, true)
}

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
