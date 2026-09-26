package gameservers

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// enshrouded implements Adapter for the Enshrouded dedicated server.
//
// Layout on the host (Root = /opt/enshrouded):
//
//	docker-compose.yml
//	.active                        -> name of the active world
//	worlds/<name>/.saveid          -> original save id of that world
//	worlds/<name>/<saveid>*        -> ring buffer of 10 slots + index
//	data/server/savegame/          -> ACTIVE world, always with the canonical id
//	data/server/enshrouded_server.json
//	data/server/backups/
//
// Critical detail of the format: the dedicated server ALWAYS loads the canonical
// save id 3ad85aea ("World 1"). A world imported with any other id is ignored and
// the server creates an empty world in its place. That is why switching worlds
// renames the files to/from the original id — it is what the enshrouded-world
// script does, and we call it here so there is a single source of truth.
type enshrouded struct{}

const enshSwitchScript = "/usr/local/bin/enshrouded-world"

func (enshrouded) Caps() Caps {
	return Caps{Worlds: true, Settings: true, Backups: true, Players: true}
}

func (enshrouded) ActiveWorld(s Server) string {
	b, err := os.ReadFile(filepath.Join(s.Root, ".active"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (e enshrouded) Worlds(s Server) ([]World, error) {
	dir := filepath.Join(s.Root, "worlds")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	active := e.ActiveWorld(s)
	out := []World{}
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		wdir := filepath.Join(dir, en.Name())
		w := World{Name: en.Name(), Active: en.Name() == active, SizeMB: dirSizeMB(wdir)}
		if b, err := os.ReadFile(filepath.Join(wdir, ".saveid")); err == nil {
			w.SaveID = strings.TrimSpace(string(b))
		}
		if fi, err := os.Stat(wdir); err == nil {
			w.Modified = fi.ModTime()
		}
		// The active world lives in data/server/savegame, not in worlds/: use the
		// mtime from there so it does not show the frozen date of the last archiving.
		if w.Active {
			if fi, err := os.Stat(filepath.Join(s.Root, "data", "server", "savegame")); err == nil {
				w.Modified = fi.ModTime()
			}
		}
		out = append(out, w)
	}
	sortWorlds(out)
	return out, nil
}

func (enshrouded) SwitchWorld(s Server, world string) error {
	if world == "" || strings.ContainsAny(world, "/\\ ") {
		return fmt.Errorf("invalid world name")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "worlds", world)); err != nil {
		return fmt.Errorf("world '%s' does not exist", world)
	}
	if _, err := os.Stat(enshSwitchScript); err != nil {
		return fmt.Errorf("script %s missing", enshSwitchScript)
	}
	// Stop + archive + install + bring up. It takes a while (the save closes
	// cleanly), hence the generous timeout.
	cmd := exec.Command(enshSwitchScript, "switch", world)
	cmd.Env = append(os.Environ(), "HOME=/root")
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		return fmt.Errorf("world switch exceeded 3 min")
	}
}

// ConfigPath exposes the config file to the raw editor.
func (enshrouded) ConfigPath(s Server) string { return enshConfigPath(s) }

func enshConfigPath(s Server) string {
	return filepath.Join(s.Root, "data", "server", "enshrouded_server.json")
}

func (enshrouded) Settings(s Server) (map[string]interface{}, error) {
	b, err := os.ReadFile(enshConfigPath(s))
	if err != nil {
		return nil, err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("invalid enshrouded_server.json: %w", err)
	}
	gs, _ := cfg["gameSettings"].(map[string]interface{})
	if gs == nil {
		gs = map[string]interface{}{}
	}
	return map[string]interface{}{
		"preset":       cfg["gameSettingsPreset"],
		"gameSettings": gs,
		"slotCount":    cfg["slotCount"],
		"name":         cfg["name"],
	}, nil
}

// SaveSettings applies a patch to gameSettings preserving the rest of the file.
//
// It forces gameSettingsPreset="Custom": with any other preset the server
// silently ignores the individual factors — the classic gotcha that makes the
// user believe they saved and nothing changed.
//
// It only writes; restarting is the caller's job (the server reads the config at boot).
func (enshrouded) SaveSettings(s Server, patch map[string]interface{}) error {
	path := enshConfigPath(s)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("invalid enshrouded_server.json: %w", err)
	}
	gs, _ := cfg["gameSettings"].(map[string]interface{})
	if gs == nil {
		gs = map[string]interface{}{}
	}
	for k, v := range patch {
		if _, known := gs[k]; !known {
			return fmt.Errorf("unknown field in gameSettings: %s", k)
		}
		gs[k] = v
	}
	cfg["gameSettings"] = gs
	cfg["gameSettingsPreset"] = "Custom"

	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}
	// The previous idiom (WriteFile + Chmod + Rename) preserved the MODE and
	// silently lost the OWNER — the container reads as 4711 and aborts the next
	// boot, far from the action that caused it. escreveAtomico takes owner, mode
	// and durability from the disk.
	return escreveAtomico(path, out, s.Root)
}

func (enshrouded) Backups(s Server) ([]Backup, error) {
	dir := filepath.Join(s.Root, "data", "server", "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []Backup{}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		fi, err := en.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{
			File:     en.Name(),
			SizeMB:   float64(fi.Size()) / 1024 / 1024,
			Modified: fi.ModTime(),
		})
	}
	// Most recent first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
