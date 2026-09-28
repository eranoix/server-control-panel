package datasaver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Settings struct {
	Enabled       bool `json:"enabled"`
	Quality       int  `json:"quality"`
	Maxdim        int  `json:"maxdim"`
	StripTrackers bool `json:"strip_trackers"`
	Greyscale     bool `json:"greyscale"`
	VideoLow      bool `json:"video_low"`
}

type Stats struct {
	Orig    int64 `json:"orig"`
	Out     int64 `json:"out"`
	Imgs    int64 `json:"imgs"`
	ReqsCut int64 `json:"reqs_cut"`
	Since   int64 `json:"since"`
}

type Status struct {
	Settings Settings `json:"settings"`
	Bypass   []string `json:"bypass"`
	Saved    Saved    `json:"saved"`
	HasCA    bool     `json:"has_ca"`
}

type Saved struct {
	Orig    int64   `json:"orig"`
	Out     int64   `json:"out"`
	Imgs    int64   `json:"imgs"`
	ReqsCut int64   `json:"reqs_cut"`
	Pct     float64 `json:"pct"`
}

type Manager struct {
	stateDir string
	caPath   string
}

func New(stateDir, caPath string) *Manager {
	return &Manager{stateDir: stateDir, caPath: caPath}
}

func (m *Manager) settingsPath() string { return filepath.Join(m.stateDir, "settings.json") }
func (m *Manager) bypassPath() string   { return filepath.Join(m.stateDir, "bypass.txt") }

var defaults = Settings{Enabled: false, Quality: 40, Maxdim: 1280, StripTrackers: true, Greyscale: false, VideoLow: true}

func (m *Manager) LoadSettings() Settings {
	s := defaults
	raw, err := os.ReadFile(m.settingsPath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(raw, &s)
	return s
}

func (m *Manager) SaveSettings(s Settings) error {
	if s.Quality < 1 || s.Quality > 100 {
		return fmt.Errorf("quality outside 1..100")
	}
	if s.Maxdim < 0 || s.Maxdim > 8192 {
		return fmt.Errorf("maxdim outside 0..8192")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWrite(m.settingsPath(), data, 0o644)
}

func (m *Manager) Bypass() []string {
	raw, err := os.ReadFile(m.bypassPath())
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		out = append(out, ln)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) SetBypass(hosts []string) error {
	seen := map[string]bool{}
	var clean []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimPrefix(h, "*.")
		if h == "" || strings.HasPrefix(h, "#") || seen[h] {
			continue
		}
		if strings.ContainsAny(h, "/ :") {
			return fmt.Errorf("invalid host: %q", h)
		}
		seen[h] = true
		clean = append(clean, h)
	}
	sort.Strings(clean)
	var b strings.Builder
	b.WriteString("# Hosts that pass through WITHOUT interception (MITM) — banks/pinning.\n")
	b.WriteString("# One host per line; subdomains included. Editable from the panel.\n")
	for _, h := range clean {
		b.WriteString(h)
		b.WriteString("\n")
	}
	return atomicWrite(m.bypassPath(), []byte(b.String()), 0o644)
}

func (m *Manager) SavedTotals() Saved {
	var agg Saved
	entries, _ := os.ReadDir(m.stateDir)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "stats-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(m.stateDir, name))
		if err != nil {
			continue
		}
		var s Stats
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		agg.Orig += s.Orig
		agg.Out += s.Out
		agg.Imgs += s.Imgs
		agg.ReqsCut += s.ReqsCut
	}
	if agg.Orig > 0 {
		agg.Pct = (1 - float64(agg.Out)/float64(agg.Orig)) * 100
	}
	return agg
}

func (m *Manager) Status() Status {
	return Status{
		Settings: m.LoadSettings(),
		Bypass:   m.Bypass(),
		Saved:    m.SavedTotals(),
		HasCA:    m.caExists(),
	}
}

func (m *Manager) caExists() bool {
	if m.caPath == "" {
		return false
	}
	fi, err := os.Stat(m.caPath)
	return err == nil && fi.Mode().IsRegular()
}

func (m *Manager) CACert() ([]byte, error) {
	if m.caPath == "" {
		return nil, fmt.Errorf("CA path not configured")
	}
	return os.ReadFile(m.caPath)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
