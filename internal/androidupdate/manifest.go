// Package androidupdate reads the incremental update catalogue of the Android
// app — the manifest that scripts/android-patches.sh writes into
// <dataDir>/android-updates/ after each publication to the F-Droid
// repository.
//
// WHY THE KEY IS THE SHA-256 OF THE BASE, NEVER THE versionCode
// A binary patch (HDiffPatch) is a function of the exact BYTES of the
// installed APK: applying the wrong patch does not give a version error, it
// gives a corrupted file. The versionCode does not derive from the content —
// two builds of the same versionCode (rebuild, re-signing, different ABI) have
// different bytes. So the app sends the SHA-256 of the APK it has installed,
// and the server only returns a patch if one exists that was generated from
// exactly that base. If none exists, the app falls back to the full artifact —
// never to an "almost right" patch.
//
// WHY THE "FULL" IS ALSO A .hdiff
// The fallback is the same hpatchz with an empty base (hdiffz "" new.apk out):
// it reconstructs bytes identical to the signed APK, fits in ~10 MB against
// the 31 MB of the raw APK, and keeps ONE single code path on the device —
// there is no "download the APK" and "apply a patch" to keep in parallel.
//
// This package is read-only and knows nothing about HTTP: what serves this is
// internal/mobilebff/update.go. What PRODUCES it is scripts/android-patches.sh.
package androidupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirName is the subdirectory of Config.DataDir where the catalogue lives. It
// has to match ANDROID_UPDATES_DIR in scripts/android-patches.sh.
const DirName = "android-updates"

// ManifestName is the index the script writes atomically on each publication.
const ManifestName = "manifest.json"

// SchemaVersion is the version of the manifest.json format. Bumping it is a
// contract change with the generator script; the server refuses a manifest of
// an unknown version instead of guessing what the fields mean.
const SchemaVersion = 1

// ErrSemManifesto means there is no update catalogue yet — the NORMAL state
// before the first publication, never a defect. Whoever serves HTTP should
// translate it into "channel not published yet", not into a 500.
var ErrSemManifesto = errors.New("androidupdate: manifest missing")

// Release describes the target signed APK (the newest published version).
// SHA256 is the hash of the **signed** APK, the same value the app computes
// from its own installed file — it is the key to the whole protocol.
type Release struct {
	VersionName string `json:"version_name"`
	VersionCode int64  `json:"version_code"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
}

// Artifact is a servable .hdiff file. File is always a path RELATIVE to the
// updates directory ("patches/<from>-<to>.hdiff" or "full/<to>.hdiff");
// SHA256/SizeBytes describe the .hdiff itself (so the app can verify the
// download), not the APK it reconstructs.
type Artifact struct {
	Kind      string `json:"kind"` // "patch" or "full"
	File      string `json:"file"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`

	// Filled in only when Kind == "patch": they identify the required base.
	FromSHA256      string `json:"from_sha256,omitempty"`
	FromVersionName string `json:"from_version_name,omitempty"`
	FromVersionCode int64  `json:"from_version_code,omitempty"`
}

// Manifest is the complete index of the update channel.
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	PackageID     string `json:"package_id"`
	// PatchTool records the exact command line (tool + compression options)
	// that generated the artifacts. The app needs an hpatchz able to decompress
	// what was used here; recording it in the manifest makes a future
	// incompatibility diagnosable instead of silent.
	PatchTool string     `json:"patch_tool"`
	Latest    Release    `json:"latest"`
	Full      Artifact   `json:"full"`
	Patches   []Artifact `json:"patches"`
}

// Dir returns the catalogue directory inside dataDir.
func Dir(dataDir string) string { return filepath.Join(dataDir, DirName) }

// Load reads and validates <dataDir>/android-updates/manifest.json.
//
// The validation is not ceremony: this manifest is the only thing that
// authorizes the server to open a file from disk by a name coming off the
// network (see OpenArtifact). A manifest with a relative File escaping the
// directory would be an arbitrary read, so File is validated here, once, at
// the entrance — and not in every handler.
func Load(dataDir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(Dir(dataDir), ManifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSemManifesto
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
	m.Latest.SHA256 = normalizaHash(m.Latest.SHA256)
	if len(m.Latest.SHA256) != 64 {
		return nil, errors.New("androidupdate: latest.sha256 is not a 64-character hex SHA-256")
	}
	if err := validaArtefato(&m.Full, "full"); err != nil {
		return nil, err
	}
	for i := range m.Patches {
		if err := validaArtefato(&m.Patches[i], "patch"); err != nil {
			return nil, err
		}
		m.Patches[i].FromSHA256 = normalizaHash(m.Patches[i].FromSHA256)
		if len(m.Patches[i].FromSHA256) != 64 {
			return nil, fmt.Errorf("androidupdate: invalid patches[%d].from_sha256", i)
		}
	}
	return &m, nil
}

func validaArtefato(a *Artifact, kind string) error {
	if a.Kind != kind {
		return fmt.Errorf("androidupdate: artifact with kind %q, expected %q", a.Kind, kind)
	}
	if err := caminhoRelativoSeguro(a.File); err != nil {
		return fmt.Errorf("androidupdate: artifact %q: %w", a.File, err)
	}
	a.SHA256 = normalizaHash(a.SHA256)
	if len(a.SHA256) != 64 {
		return fmt.Errorf("androidupdate: artifact %q has no valid sha256", a.File)
	}
	if a.SizeBytes <= 0 {
		return fmt.Errorf("androidupdate: artifact %q has an invalid size_bytes", a.File)
	}
	return nil
}

// caminhoRelativoSeguro refuses any File that is not a relative, clean path
// contained within the updates directory.
func caminhoRelativoSeguro(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return errors.New("absolute path is not allowed")
	}
	limpo := filepath.Clean(p)
	if limpo != p {
		return errors.New("path not normalized")
	}
	if limpo == ".." || strings.HasPrefix(limpo, ".."+string(filepath.Separator)) {
		return errors.New("path escapes the updates directory")
	}
	return nil
}

func normalizaHash(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// UpToDate reports whether the APK whose base is baseSHA256 already IS the newest version.
func (m *Manifest) UpToDate(baseSHA256 string) bool {
	b := normalizaHash(baseSHA256)
	return b != "" && b == m.Latest.SHA256
}

// PatchFor returns the patch generated exactly from baseSHA256, or nil if
// there is none — in which case the caller should offer Full. It never
// approximates: an unknown base (a version outside the retention window, a
// local build, an APK from another origin) simply has no patch.
func (m *Manifest) PatchFor(baseSHA256 string) *Artifact {
	b := normalizaHash(baseSHA256)
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

// ArtifactByFile resolves a file name coming off the network against the list
// of artifacts the manifest declares. It is the ONLY door through which an
// external name becomes a path on disk: exact string comparison against the
// catalogue, not interpretation of the path. A name that is not in the
// manifest does not exist, even if the file exists on disk.
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

// OpenArtifact opens for reading the artifact named `file`, if and only if it
// appears in the manifest. It also returns the catalogue descriptor, so the
// caller can send an ETag/Content-Length consistent with what it promised.
// The *os.File is the caller's to close.
func OpenArtifact(dataDir, file string) (*os.File, os.FileInfo, *Artifact, error) {
	m, err := Load(dataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	art := m.ArtifactByFile(file)
	if art == nil {
		return nil, nil, nil, os.ErrNotExist
	}
	caminho := filepath.Join(Dir(dataDir), art.File)
	f, err := os.Open(caminho)
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
