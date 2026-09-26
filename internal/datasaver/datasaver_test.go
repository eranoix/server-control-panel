package datasaver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsValidation(t *testing.T) {
	m := New(t.TempDir(), "")
	if err := m.SaveSettings(Settings{Quality: 0}); err == nil {
		t.Fatal("quality 0 should fail")
	}
	if err := m.SaveSettings(Settings{Quality: 101}); err == nil {
		t.Fatal("quality 101 should fail")
	}
	if err := m.SaveSettings(Settings{Quality: 40, Maxdim: 1280}); err != nil {
		t.Fatalf("a valid settings failed: %v", err)
	}
	got := m.LoadSettings()
	if got.Quality != 40 || got.Maxdim != 1280 {
		t.Fatalf("wrong round-trip: %+v", got)
	}
}

func TestBypassSanitize(t *testing.T) {
	m := New(t.TempDir(), "")
	err := m.SetBypass([]string{"  ITAU.com.br ", "*.nubank.com.br", "itau.com.br", "# comment", ""})
	if err != nil {
		t.Fatal(err)
	}
	got := m.Bypass()
	// dedup + lowercase + strip "*." → itau.com.br, nubank.com.br
	if len(got) != 2 {
		t.Fatalf("expected 2 clean hosts, got %v", got)
	}
	if err := m.SetBypass([]string{"bad host/path"}); err == nil {
		t.Fatal("a host with a space/slash should fail")
	}
}

func TestSavedTotals(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "stats-vps.json"), []byte(`{"orig":100,"out":25,"imgs":2,"reqs_cut":3}`), 0o644)
	os.WriteFile(filepath.Join(dir, "stats-casa.json"), []byte(`{"orig":100,"out":25,"imgs":1,"reqs_cut":1}`), 0o644)
	m := New(dir, "")
	s := m.SavedTotals()
	if s.Orig != 200 || s.Out != 50 || s.Imgs != 3 || s.ReqsCut != 4 {
		t.Fatalf("wrong aggregation: %+v", s)
	}
	if s.Pct < 74.9 || s.Pct > 75.1 {
		t.Fatalf("expected pct ~75, got %.2f", s.Pct)
	}
}

// TestDefaultsDisabled locks the data-saver OFF by default. Being born on
// routes the device's web traffic through a MITM that, without the CA installed on it, breaks
// ALL HTTPS — it has already taken the work tunnel down. The default can never be true.
func TestDefaultsDisabled(t *testing.T) {
	if defaults.Enabled {
		t.Fatal("defaults.Enabled must be false: data-saver cannot be born switched on (MITM breaks HTTPS without a CA)")
	}
	// LoadSettings with no file falls back to the default — it has to come back off.
	m := New(t.TempDir(), "")
	if m.LoadSettings().Enabled {
		t.Fatal("LoadSettings with no settings.json must return Enabled=false")
	}
}
