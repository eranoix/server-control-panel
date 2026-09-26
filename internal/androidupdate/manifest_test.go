package androidupdate

// manifest_test.go — Load() is the boundary where a file on disk becomes the
// allowlist that authorizes the server to open files by a name coming off the
// network. The tests below cover what it has to REFUSE, besides the happy path.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shaNovo  = "1111111111111111111111111111111111111111111111111111111111111111"
	shaBase  = "2222222222222222222222222222222222222222222222222222222222222222"
	shaFull  = "3333333333333333333333333333333333333333333333333333333333333333"
	shaPatch = "4444444444444444444444444444444444444444444444444444444444444444"
)

func manifestoValido() Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   "2026-09-06T00:00:00Z",
		PackageID:     "tech.northwind.vpsm.app",
		PatchTool:     "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m",
		Latest:        Release{VersionName: "0.1.6", VersionCode: 6, SHA256: shaNovo, SizeBytes: 31135416},
		Full:          Artifact{Kind: "full", File: "full/" + shaNovo + ".hdiff", SizeBytes: 10029237, SHA256: shaFull},
		Patches: []Artifact{{
			Kind: "patch", File: "patches/" + shaBase + "-" + shaNovo + ".hdiff",
			SizeBytes: 1400329, SHA256: shaPatch,
			FromSHA256: shaBase, FromVersionName: "0.1.5", FromVersionCode: 5,
		}},
	}
}

func gravaManifesto(t *testing.T, m Manifest) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.MkdirAll(Dir(dataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(dataDir), ManifestName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestLoad_SemManifesto(t *testing.T) {
	_, err := Load(t.TempDir())
	if !errors.Is(err, ErrSemManifesto) {
		t.Fatalf("err = %v, want ErrSemManifesto — before the first release this is a normal state, not a defect", err)
	}
}

func TestLoad_CaminhoFeliz(t *testing.T) {
	m, err := Load(gravaManifesto(t, manifestoValido()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p := m.PatchFor(shaBase); p == nil || p.SizeBytes != 1400329 {
		t.Fatalf("PatchFor(base) = %+v", p)
	}
	if m.PatchFor(shaNovo) != nil {
		t.Fatal("PatchFor(latest) returned a patch — whoever is already up to date has nowhere to go")
	}
	if m.PatchFor("") != nil {
		t.Fatal("PatchFor(\"\") returned a patch")
	}
	if !m.UpToDate(strings.ToUpper(shaNovo)) {
		t.Fatal("UpToDate did not normalize the case of the hash")
	}
	if m.UpToDate("") {
		t.Fatal("UpToDate(\"\") = true — a missing baseline is not 'already up to date'")
	}
}

// TestLoad_RecusaManifestoPerigosoOuIncoerente covers, in a single place,
// everything that would make the allowlist unsafe or untruthful. Each case is a
// manifest the server must NEVER accept — refusing the whole file is safer than
// filtering out the bad entry and serving the rest.
func TestLoad_RecusaManifestoPerigosoOuIncoerente(t *testing.T) {
	casos := map[string]func(m *Manifest){
		"schema desconhecido":         func(m *Manifest) { m.SchemaVersion = 99 },
		"full com caminho absoluto":   func(m *Manifest) { m.Full.File = "/etc/passwd" },
		"full escapando do diretorio": func(m *Manifest) { m.Full.File = "../../secrets.vault" },
		"full nao normalizado":        func(m *Manifest) { m.Full.File = "full/./x.hdiff" },
		"full com kind errado":        func(m *Manifest) { m.Full.Kind = "patch" },
		"full sem sha":                func(m *Manifest) { m.Full.SHA256 = "" },
		"full com tamanho zero":       func(m *Manifest) { m.Full.SizeBytes = 0 },
		"latest sem sha valido":       func(m *Manifest) { m.Latest.SHA256 = "abc" },
		"patch com caminho absoluto":  func(m *Manifest) { m.Patches[0].File = "/etc/shadow" },
		"patch com base invalida":     func(m *Manifest) { m.Patches[0].FromSHA256 = "xyz" },
		"patch com kind errado":       func(m *Manifest) { m.Patches[0].Kind = "full" },
	}
	for nome, quebra := range casos {
		t.Run(nome, func(t *testing.T) {
			m := manifestoValido()
			quebra(&m)
			if _, err := Load(gravaManifesto(t, m)); err == nil {
				t.Fatal("Load accepted a manifest it should have refused")
			}
		})
	}
}

func TestLoad_ManifestoMalformado(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(Dir(dataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(dataDir), ManifestName), []byte("{nao e json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dataDir); err == nil {
		t.Fatal("Load accepted invalid JSON")
	}
}

// TestArtifactByFile_SoOQueEstaNoManifesto — the comparison is exact string
// matching against the catalogue, never interpretation of the path.
func TestArtifactByFile_SoOQueEstaNoManifesto(t *testing.T) {
	m, err := Load(gravaManifesto(t, manifestoValido()))
	if err != nil {
		t.Fatal(err)
	}
	if m.ArtifactByFile(m.Full.File) == nil {
		t.Fatal("the manifest's own artifact was not found")
	}
	if m.ArtifactByFile(m.Patches[0].File) == nil {
		t.Fatal("the manifest's own patch was not found")
	}
	for _, nome := range []string{"", "full/outro.hdiff", "../secrets.vault", "/etc/passwd", "FULL/" + shaNovo + ".hdiff"} {
		if m.ArtifactByFile(nome) != nil {
			t.Fatalf("ArtifactByFile(%q) returned an artifact", nome)
		}
	}
}

func TestOpenArtifact_RecusaOQueNaoEstaNoManifesto(t *testing.T) {
	dataDir := gravaManifesto(t, manifestoValido())
	if err := os.MkdirAll(filepath.Join(Dir(dataDir), "full"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "secrets.vault"), []byte("SEGREDO"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, nome := range []string{"../secrets.vault", "/etc/passwd", "full/inexistente.hdiff"} {
		f, _, _, err := OpenArtifact(dataDir, nome)
		if err == nil {
			f.Close()
			t.Fatalf("OpenArtifact(%q) opened something", nome)
		}
	}
}
