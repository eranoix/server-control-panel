// Package datasaver manages the content-transformation proxy's shared state on
// the host: the tunable settings (quality/maxdim/greyscale/tracker-strip), the
// bypass list (banks/pinning that must NOT be MITM'd), the CA cert offered for
// download, and the cumulative bytes-saved counters written by each proxy
// instance.
//
// The proxy itself is mitmproxy (Docker, two instances: -vps and -casa). This
// package is only the host-side file plane the panel (Segurança → Economia)
// reads and edits — it does not talk to the proxy over the network. Settings
// are hot-reloaded by the addon on every request; the bypass list is read at
// container start, so changing it needs a proxy restart (the caller does that).
package datasaver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Settings are the addon's tunables (mirrors state/settings.json).
type Settings struct {
	Enabled       bool `json:"enabled"`        // feature: images → WebP
	Quality       int  `json:"quality"`        // WebP quality 1..100 (lower = more saving)
	Maxdim        int  `json:"maxdim"`         // downscale of the longest side, px (0 = none)
	StripTrackers bool `json:"strip_trackers"` // feature: cuts tracker domains + Referer
	Greyscale     bool `json:"greyscale"`      // feature: greyscale (extreme saving)
	VideoLow      bool `json:"video_low"`      // feature: forces low resolution on video (HLS/DASH)
}

// Stats is the cumulative saving counter one proxy instance writes.
type Stats struct {
	Orig    int64 `json:"orig"`     // original bytes of the transcoded images
	Out     int64 `json:"out"`      // bytes after WebP
	Imgs    int64 `json:"imgs"`     // number of compressed images
	ReqsCut int64 `json:"reqs_cut"` // tracker requests cut (204)
	Since   int64 `json:"since"`    // unix time when counting started
}

// Status is the whole panel payload.
type Status struct {
	Settings Settings `json:"settings"`
	Bypass   []string `json:"bypass"`
	Saved    Saved    `json:"saved"`
	HasCA    bool     `json:"has_ca"`
}

// Saved is the aggregate of every instance's Stats.
type Saved struct {
	Orig    int64   `json:"orig"`
	Out     int64   `json:"out"`
	Imgs    int64   `json:"imgs"`
	ReqsCut int64   `json:"reqs_cut"`
	Pct     float64 `json:"pct"` // 1 - out/orig, in %
}

// Manager owns the state directory + CA path.
type Manager struct {
	stateDir string
	caPath   string
}

func New(stateDir, caPath string) *Manager {
	return &Manager{stateDir: stateDir, caPath: caPath}
}

func (m *Manager) settingsPath() string { return filepath.Join(m.stateDir, "settings.json") }
func (m *Manager) bypassPath() string   { return filepath.Join(m.stateDir, "bypass.txt") }

// defaults are born OFF on purpose: saving turns on the compression MITM on the
// device's web path, and without the data-saver CA installed ON IT all HTTPS
// breaks (TLS handshake rejected). Being born on has already taken the tunnel down — the
// data-saver only comes into play by an explicit act (toggle with ca_ack).
var defaults = Settings{Enabled: false, Quality: 40, Maxdim: 1280, StripTrackers: true, Greyscale: false, VideoLow: true}

// LoadSettings reads settings.json, falling back to defaults per-field.
func (m *Manager) LoadSettings() Settings {
	s := defaults
	raw, err := os.ReadFile(m.settingsPath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(raw, &s) // tolerate partial/corrupt
	return s
}

// SaveSettings validates and atomically writes settings.json. The addon
// hot-reloads it per request, so no proxy restart is needed.
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

// Bypass reads the bypass host list (skips blanks/comments).
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

// SetBypass rewrites bypass.txt from a clean host list (a header comment is
// preserved). The proxies read this at start — the caller must restart them.
func (m *Manager) SetBypass(hosts []string) error {
	seen := map[string]bool{}
	var clean []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimPrefix(h, "*.")
		if h == "" || strings.HasPrefix(h, "#") || seen[h] {
			continue
		}
		// rudimentary host sanity: no scheme, no slash, has a dot or is a TLD-ish token
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

// Saved aggregates every stats-*.json in the state dir.
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

// Status assembles the full panel payload.
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

// CACert returns the CA certificate bytes to hand the browser for download.
func (m *Manager) CACert() ([]byte, error) {
	if m.caPath == "" {
		return nil, fmt.Errorf("CA path not configured")
	}
	return os.ReadFile(m.caPath)
}

// atomicWrite writes via temp-in-same-dir + rename.
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
