package mobilebff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"server-control-panel/internal/androidupdate"
	"server-control-panel/internal/config"
)

type labUpdate struct {
	dataDir     string
	shaAPKNew   string
	shaAPKBase  string
	fullContent []byte
	fullFile    string
	patchFile   string
}

func buildLabUpdate(t *testing.T) labUpdate {
	t.Helper()
	dataDir := t.TempDir()
	dir := androidupdate.Dir(dataDir)
	for _, sub := range []string{"patches", "full"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	shaAPKNew := hashHex([]byte("apk-new-version"))
	shaAPKBase := hashHex([]byte("apk-old-version"))

	fullContent := bytes.Repeat([]byte("F"), 1000)
	patchContent := bytes.Repeat([]byte("P"), 300)

	fullFile := "full/" + shaAPKNew + ".hdiff"
	patchFile := "patches/" + shaAPKBase + "-" + shaAPKNew + ".hdiff"
	writeOut(t, filepath.Join(dir, fullFile), fullContent)
	writeOut(t, filepath.Join(dir, patchFile), patchContent)

	m := androidupdate.Manifest{
		SchemaVersion: androidupdate.SchemaVersion,
		GeneratedAt:   "2026-09-06T00:00:00Z",
		PackageID:     "tech.northwind.servercontrolpanel",
		PatchTool:     "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m",
		Latest: androidupdate.Release{
			VersionName: "0.1.6",
			VersionCode: 6,
			SHA256:      shaAPKNew,
			SizeBytes:   31135416,
		},
		Full: androidupdate.Artifact{
			Kind: "full", File: fullFile,
			SizeBytes: int64(len(fullContent)), SHA256: hashHex(fullContent),
		},
		Patches: []androidupdate.Artifact{{
			Kind: "patch", File: patchFile,
			SizeBytes: int64(len(patchContent)), SHA256: hashHex(patchContent),
			FromSHA256: shaAPKBase, FromVersionName: "0.1.5", FromVersionCode: 5,
		}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeOut(t, filepath.Join(dir, androidupdate.ManifestName), raw)

	return labUpdate{
		dataDir: dataDir, shaAPKNew: shaAPKNew, shaAPKBase: shaAPKBase,
		fullContent: fullContent, fullFile: fullFile, patchFile: patchFile,
	}
}

func writeOut(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hashHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func requestUpdate(t *testing.T, dataDir, query string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: dataDir}})
	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/app/update"+query, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeUpdate(t *testing.T, rec *httptest.ResponseRecorder) AppUpdateResponse {
	t.Helper()
	var got AppUpdateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v; body=%s", err, rec.Body.String())
	}
	return got
}

func TestAppUpdate_KnownBase_ReturnsPatch(t *testing.T) {
	lab := buildLabUpdate(t)
	rec := requestUpdate(t, lab.dataDir, "?base_sha256="+lab.shaAPKBase)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeUpdate(t, rec)
	if got.UpToDate {
		t.Fatal("up_to_date = true for an old base")
	}
	if got.Patch == nil {
		t.Fatal("patch = null for a base that DOES have a generated patch")
	}
	if got.Patch.SizeBytes != 300 {
		t.Fatalf("patch.size_bytes = %d, want 300", got.Patch.SizeBytes)
	}
	wantURL := Prefix + artifactPath + "?file=" + url.QueryEscape(lab.patchFile)
	if got.Patch.URL != wantURL {
		t.Fatalf("patch.url = %q, want %q", got.Patch.URL, wantURL)
	}
	if got.Full.SizeBytes != 1000 {
		t.Fatalf("full.size_bytes = %d, want 1000", got.Full.SizeBytes)
	}
	if got.Latest.SHA256 != lab.shaAPKNew || got.Latest.VersionCode != 6 {
		t.Fatalf("unexpected latest: %+v", got.Latest)
	}
	if got.PatchTool == "" {
		t.Fatal("patch_tool empty — the format-incompatibility diagnosis is lost")
	}
}

func TestAppUpdate_UnknownBase_NullPatchAndFullPresent(t *testing.T) {
	lab := buildLabUpdate(t)
	unknown := hashHex([]byte("apk-the-server-never-saw"))
	rec := requestUpdate(t, lab.dataDir, "?base_sha256="+unknown)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeUpdate(t, rec)
	if got.Patch != nil {
		t.Fatalf("patch = %+v for an unknown base — should be null", got.Patch)
	}
	if got.UpToDate {
		t.Fatal("up_to_date = true for an unknown base")
	}
	if got.Full.SHA256 == "" || got.Full.URL == "" {
		t.Fatalf("incomplete full: %+v", got.Full)
	}
}

func TestAppUpdate_NoBase_OnlyFullPath(t *testing.T) {
	lab := buildLabUpdate(t)
	got := decodeUpdate(t, requestUpdate(t, lab.dataDir, ""))
	if got.Patch != nil {
		t.Fatalf("patch = %+v without base_sha256 — should be null", got.Patch)
	}
	if got.UpToDate {
		t.Fatal("up_to_date = true without base_sha256")
	}
}

func TestAppUpdate_AlreadyOnLatestVersion(t *testing.T) {
	lab := buildLabUpdate(t)
	got := decodeUpdate(t, requestUpdate(t, lab.dataDir, "?base_sha256="+lab.shaAPKNew))
	if !got.UpToDate {
		t.Fatal("up_to_date = false for someone who already has the newest APK")
	}
	if got.Patch != nil {
		t.Fatalf("patch = %+v for someone who is already on the latest version", got.Patch)
	}
}

func TestAppUpdate_UppercaseHash(t *testing.T) {
	lab := buildLabUpdate(t)
	upper := ""
	for _, r := range lab.shaAPKBase {
		if r >= 'a' && r <= 'f' {
			r = r - 'a' + 'A'
		}
		upper += string(r)
	}
	got := decodeUpdate(t, requestUpdate(t, lab.dataDir, "?base_sha256="+upper))
	if got.Patch == nil {
		t.Fatal("patch = null with the same hash in uppercase")
	}
}

func TestAppUpdate_NoManifest_503(t *testing.T) {
	rec := requestUpdate(t, t.TempDir(), "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppUpdate_Unauthenticated_401(t *testing.T) {
	lab := buildLabUpdate(t)
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: lab.dataDir}})

	for _, target := range []string{
		"/api/mobile/v1/app/update?base_sha256=" + lab.shaAPKBase,
		"/api/mobile/v1/app/update/artifact?file=" + url.QueryEscape(lab.fullFile),
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", target, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("hdiff")) {
			t.Fatalf("%s: unauthenticated response leaks artifact name: %s", target, rec.Body.String())
		}
	}
}

func requestArtifact(t *testing.T, dataDir, file string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: dataDir}})
	req := newAuthedRequest(http.MethodGet,
		"/api/mobile/v1/app/update/artifact?file="+url.QueryEscape(file), nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAppUpdateArtifact_Range_206(t *testing.T) {
	lab := buildLabUpdate(t)
	rec := requestArtifact(t, lab.dataDir, lab.fullFile, map[string]string{"Range": "bytes=100-199"})

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body=%s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("bytes 100-199/%d", len(lab.fullContent))
	if got := rec.Header().Get("Content-Range"); got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), lab.fullContent[100:200]) {
		t.Fatalf("body of %d bytes is not the requested chunk", rec.Body.Len())
	}
}

func TestAppUpdateArtifact_ResumeFromMiddle(t *testing.T) {
	lab := buildLabUpdate(t)
	rec := requestArtifact(t, lab.dataDir, lab.fullFile, map[string]string{"Range": "bytes=600-"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if rec.Body.Len() != 400 {
		t.Fatalf("body = %d bytes, want 400", rec.Body.Len())
	}
	if !bytes.Equal(rec.Body.Bytes(), lab.fullContent[600:]) {
		t.Fatal("resumed bytes do not match the tail of the file")
	}
}

func TestAppUpdateArtifact_NoRange_Full200(t *testing.T) {
	lab := buildLabUpdate(t)
	rec := requestArtifact(t, lab.dataDir, lab.fullFile, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != len(lab.fullContent) {
		t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(lab.fullContent))
	}
	if got := rec.Header().Get("ETag"); got != `"`+hashHex(lab.fullContent)+`"` {
		t.Fatalf("ETag = %q — needs to be the artifact's sha256 for the resume's If-Range to work", got)
	}
}

func TestAppUpdateArtifact_UnsatisfiableRange_416(t *testing.T) {
	lab := buildLabUpdate(t)
	rec := requestArtifact(t, lab.dataDir, lab.fullFile, map[string]string{"Range": "bytes=99999-199999"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", rec.Code)
	}
}

func TestAppUpdateArtifact_NotInManifest_404(t *testing.T) {
	lab := buildLabUpdate(t)

	writeOut(t, filepath.Join(androidupdate.Dir(lab.dataDir), "full", "intruder.hdiff"), []byte("x"))
	writeOut(t, filepath.Join(lab.dataDir, "secrets.vault"), []byte("SECRET"))

	for _, name := range []string{
		"full/intruder.hdiff",
		"../secrets.vault",
		"../../etc/passwd",
		"/etc/passwd",
		"full/../../secrets.vault",
		"",
	} {
		rec := requestArtifact(t, lab.dataDir, name, nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("file=%q returned 200 — the manifest allowlist did not hold", name)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("SECRET")) {
			t.Fatalf("file=%q leaked content from outside the updates directory", name)
		}
	}
}
