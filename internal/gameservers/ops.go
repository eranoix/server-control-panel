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

// Maintenance operations: installed build, update, raw config, world
// export/import and editing the server inventory.

// BuildInfo reads the installed build from the SteamCMD manifest.
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

// UpdateNow triggers the update check.
//
// The image runs steamcmd at container start, so restarting IS the update
// trigger — there is no "update without restarting" path. The UI says so.
func (m *Manager) UpdateNow(ctx context.Context, s Server) error {
	return m.Action(ctx, s, "restart")
}

// RawConfig returns the game's configuration file as text.
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

// SaveRawConfig validates as JSON before writing and keeps a .bak. Without the
// validation, a misplaced comma would leave the server unable to come up.
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
	// The `.bak` also goes through the helper, with `ref` = the ORIGINAL file. A
	// root:root .bak beside a 4711 config is the same class of defect, only
	// quieter — nobody looks at a backup's owner until they need it.
	if old, err := os.ReadFile(p); err == nil {
		_ = escreveAtomico(p+".bak", old, p)
	}
	return escreveAtomico(p, []byte(text), p)
}

// ExportWorld zips a world's folder into a temporary file.
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

// ImportWorld extracts a zip as a new world. It refuses to overwrite.
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

	// Work out the save id from the file names: a world is a family
	// <id>, <id>-1..-9, <id>-index, <id>_info*. Without this the panel does not
	// know which id to rename to when activating it.
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
		base := filepath.Base(f.Name) // Zip Slip: never trust the path from inside
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
	if err := escreveAtomico(filepath.Join(dst, ".saveid"), []byte(saveID+"\n"), s.Root); err != nil {
		return err
	}
	// The owner comes from OBSERVING the disk, never from a constant. The value
	// that used to be here — 4711 — is Enshrouded's uid; Palworld uses PUID 1000,
	// and that is exactly how the defect was born the first time. A new constant
	// in the agent is how the same defect reappears under another name.
	//
	// The reference is s.Root, and not `dst`'s immediate parent: the parent may be
	// a directory the panel itself just created, already root:root. The server's
	// root is the only point of the tree whose owner is reliably the game's.
	return chownComoRef(dst, s.Root, true)
}

// ── Server inventory ───────────────────────────────────────────────────────

// SaveInventory replaces gameservers.json and reloads it in memory.
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
		// The root is checked on disk ONLY when the server lives on THIS host.
		//
		// 🔴 The unconditional `os.Stat` that used to be here made it impossible to
		// register a server that lives on ANOTHER node — which is exactly the model
		// this work exists to build. CT 201's `/opt/jogo-b` path does not exist on
		// the panel's disk, and should not: the one who sees it is the agent there.
		// The check refused the registration with "the folder does not exist", a
		// message that was true about the WRONG machine.
		//
		// Kept for a server with no node (the local case), because there it catches
		// the typo at registration time, far from the "restart" that would only fail
		// later. For a server with a node the panel HAS NO WAY of checking from
		// here, and pretending to check would be worse: it would approve a wrong
		// path with an air of being validated. What flags a wrong path on a remote
		// node is the first operation, its error coming from the agent, naming the machine.
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
	// This one belongs to the PANEL, not the container — the owner matters less.
	// What matters here is DURABILITY: without fsync, a power cut inside the ZFS
	// txg window costs the whole inventory. `ref` is the file itself.
	if err := escreveAtomico(m.path, b, m.path); err != nil {
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
