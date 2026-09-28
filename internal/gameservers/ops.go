package gameservers

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type BuildInfo struct {
	BuildID     string  `json:"buildId"`
	LastUpdated int64   `json:"lastUpdated"`
	SizeMB      float64 `json:"sizeMb"`
}

var acfKey = regexp.MustCompile(`"(\w+)"\s+"([^"]*)"`)

func (m *Manager) Build(s Server) BuildInfo {
	var out BuildInfo
	dir := filepath.Join(s.Root, "data", "server", "steamapps")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "appmanifest_") || !strings.HasSuffix(e.Name(), ".acf") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if mm := acfKey.FindStringSubmatch(sc.Text()); len(mm) == 3 {
				switch mm[1] {
				case "buildid":
					out.BuildID = mm[2]
				case "LastUpdated":
					out.LastUpdated, _ = strconv.ParseInt(mm[2], 10, 64)
				case "SizeOnDisk":
					if n, err := strconv.ParseFloat(mm[2], 64); err == nil {
						out.SizeMB = n / 1024 / 1024
					}
				}
			}
		}
		f.Close()
		break
	}
	return out
}

func (m *Manager) UpdateNow(ctx context.Context, s Server) error {
	return m.Action(ctx, s, "restart")
}

func (m *Manager) RawConfig(s Server) (string, string, error) {
	p := m.adapter(s).ConfigPath(s)
	if p == "" {
		return "", "", fmt.Errorf("this game exposes no configuration file")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", "", err
	}
	return string(b), p, nil
}

func (m *Manager) SaveRawConfig(s Server, text string) error {
	p := m.adapter(s).ConfigPath(s)
	if p == "" {
		return fmt.Errorf("this game exposes no configuration file")
	}
	var probe map[string]interface{}
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return fmt.Errorf("invalid JSON — nothing was written: %v", err)
	}
	if _, ok := probe["userGroups"]; !ok {
		return fmt.Errorf("the file lost userGroups — refusing so access is not locked out")
	}
	if old, err := os.ReadFile(p); err == nil {
		_ = writeAtomic(p+".bak", old, p)
	}
	return writeAtomic(p, []byte(text), p)
}

func (m *Manager) ExportWorld(s Server, name string) (string, error) {
	if err := safeName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(s.Root, "worlds", name)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("world '%s' does not exist", name)
	}
	tmp, err := os.CreateTemp("", "world-"+name+"-*.zip")
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(tmp)
	entries, err := os.ReadDir(dir)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := zipAdd(zw, filepath.Join(dir, e.Name()), e.Name()); err != nil {
			zw.Close()
			tmp.Close()
			os.Remove(tmp.Name())
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

func (m *Manager) ImportWorld(s Server, name, zipPath string) error {
	if err := safeName(name); err != nil {
		return err
	}
	dst := filepath.Join(s.Root, "worlds", name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("a world named '%s' already exists", name)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("file is not a valid zip")
	}
	defer zr.Close()

	saveID := ""
	for _, f := range zr.File {
		b := filepath.Base(f.Name)
		if i := strings.Index(b, "-index"); i > 0 {
			saveID = b[:i]
			break
		}
	}
	if saveID == "" {
		return fmt.Errorf("the zip does not look like an Enshrouded world (the -index file is missing)")
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		if err := safeName(base); err != nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			os.RemoveAll(dst)
			return err
		}
		out, err := os.Create(filepath.Join(dst, base))
		if err != nil {
			rc.Close()
			os.RemoveAll(dst)
			return err
		}
		_, cerr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if cerr != nil {
			os.RemoveAll(dst)
			return cerr
		}
	}
	if err := writeAtomic(filepath.Join(dst, ".saveid"), []byte(saveID+"\n"), s.Root); err != nil {
		return err
	}
	return chownLikeRef(dst, s.Root, true)
}

func (m *Manager) SaveInventory(list []Server) error {
	seen := map[string]bool{}
	for i := range list {
		s := &list[i]
		s.ID = strings.TrimSpace(s.ID)
		s.Name = strings.TrimSpace(s.Name)
		s.Game = strings.TrimSpace(s.Game)
		s.Container = strings.TrimSpace(s.Container)
		s.Root = strings.TrimSpace(s.Root)
		if s.ID == "" || strings.ContainsAny(s.ID, "/\\ ") {
			return fmt.Errorf("invalid id: %q (no spaces or slashes)", s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate id: %s", s.ID)
		}
		seen[s.ID] = true
		if s.Name == "" {
			return fmt.Errorf("%s: name cannot be empty", s.ID)
		}
		if s.Container == "" {
			return fmt.Errorf("%s: provide the container name", s.ID)
		}
		if !filepath.IsAbs(s.Root) {
			return fmt.Errorf("%s: the root must be an absolute path", s.ID)
		}
		if s.No == "" {
			if _, err := os.Stat(s.Root); err != nil {
				return fmt.Errorf("%s: folder %s does not exist on this host (a server with no node is treated as local)", s.ID, s.Root)
			}
		}
		if _, ok := adapters[s.Game]; !ok {
			return fmt.Errorf("%s: unsupported game: %q (available: %s)", s.ID, s.Game, knownGames())
		}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(m.path, b, m.path); err != nil {
		return err
	}
	return m.Reload()
}

func knownGames() string {
	out := []string{}
	for k := range adapters {
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}
