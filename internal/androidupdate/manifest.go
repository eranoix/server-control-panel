package androidupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DirName = "android-updates"

const ManifestName = "manifest.json"

const SchemaVersion = 1

var ErrNoManifest = errors.New("androidupdate: manifest missing")

type Release struct {
	VersionName string `json:"version_name"`
	VersionCode int64  `json:"version_code"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
}

type Artifact struct {
	Kind      string `json:"kind"`
	File      string `json:"file"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`

	FromSHA256      string `json:"from_sha256,omitempty"`
	FromVersionName string `json:"from_version_name,omitempty"`
	FromVersionCode int64  `json:"from_version_code,omitempty"`
}

type Manifest struct {
	SchemaVersion int        `json:"schema_version"`
	GeneratedAt   string     `json:"generated_at"`
	PackageID     string     `json:"package_id"`
	PatchTool     string     `json:"patch_tool"`
	Latest        Release    `json:"latest"`
	Full          Artifact   `json:"full"`
	Patches       []Artifact `json:"patches"`
}

func Dir(dataDir string) string { return filepath.Join(dataDir, DirName) }

func Load(dataDir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(Dir(dataDir), ManifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoManifest
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("androidupdate: malformed manifest: %w", err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("androidupdate: unknown schema_version %d (expected %d)", m.SchemaVersion, SchemaVersion)
	}
	m.Latest.SHA256 = normalizeHash(m.Latest.SHA256)
	if len(m.Latest.SHA256) != 64 {
		return nil, errors.New("androidupdate: latest.sha256 is not a 64-character hex SHA-256")
	}
	if err := validateArtifact(&m.Full, "full"); err != nil {
		return nil, err
	}
	for i := range m.Patches {
		if err := validateArtifact(&m.Patches[i], "patch"); err != nil {
			return nil, err
		}
		m.Patches[i].FromSHA256 = normalizeHash(m.Patches[i].FromSHA256)
		if len(m.Patches[i].FromSHA256) != 64 {
			return nil, fmt.Errorf("androidupdate: invalid patches[%d].from_sha256", i)
		}
	}
	return &m, nil
}

func validateArtifact(a *Artifact, kind string) error {
	if a.Kind != kind {
		return fmt.Errorf("androidupdate: artifact with kind %q, expected %q", a.Kind, kind)
	}
	if err := safeRelativePath(a.File); err != nil {
		return fmt.Errorf("androidupdate: artifact %q: %w", a.File, err)
	}
	a.SHA256 = normalizeHash(a.SHA256)
	if len(a.SHA256) != 64 {
		return fmt.Errorf("androidupdate: artifact %q has no valid sha256", a.File)
	}
	if a.SizeBytes <= 0 {
		return fmt.Errorf("androidupdate: artifact %q has an invalid size_bytes", a.File)
	}
	return nil
}

func safeRelativePath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return errors.New("absolute path is not allowed")
	}
	clean := filepath.Clean(p)
	if clean != p {
		return errors.New("path not normalized")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes the updates directory")
	}
	return nil
}

func normalizeHash(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (m *Manifest) UpToDate(baseSHA256 string) bool {
	b := normalizeHash(baseSHA256)
	return b != "" && b == m.Latest.SHA256
}

func (m *Manifest) PatchFor(baseSHA256 string) *Artifact {
	b := normalizeHash(baseSHA256)
	if b == "" || b == m.Latest.SHA256 {
		return nil
	}
	for i := range m.Patches {
		if m.Patches[i].FromSHA256 == b {
			return &m.Patches[i]
		}
	}
	return nil
}

func (m *Manifest) ArtifactByFile(file string) *Artifact {
	if file == "" {
		return nil
	}
	if m.Full.File == file {
		return &m.Full
	}
	for i := range m.Patches {
		if m.Patches[i].File == file {
			return &m.Patches[i]
		}
	}
	return nil
}

func OpenArtifact(dataDir, file string) (*os.File, os.FileInfo, *Artifact, error) {
	m, err := Load(dataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	art := m.ArtifactByFile(file)
	if art == nil {
		return nil, nil, nil, os.ErrNotExist
	}
	path := filepath.Join(Dir(dataDir), art.File)
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	if fi.IsDir() {
		f.Close()
		return nil, nil, nil, os.ErrNotExist
	}
	return f, fi, art, nil
}
