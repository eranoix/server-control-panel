package mobilebff

// update_test.go — covers the app's incremental update channel from the HTTP
// side: patch selection by the base's SHA-256, fallback to the full path, the
// authentication gate, and — what matters most on a bad connection — that the
// byte route answers Range for real (206 + Content-Range + the right slice),
// and not a 200 with the whole file.

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

// labUpdate builds a complete and coherent update catalogue on disk (two real
// artifacts, with real hashes) and returns the dataDir plus the hashes the
// tests need to quote.
type labUpdate struct {
	dataDir      string
	shaAPKNovo   string
	shaAPKBase   string
	conteudoFull []byte
	arquivoFull  string
	arquivoPatch string
}

func montaLabUpdate(t *testing.T) labUpdate {
	t.Helper()
	dataDir := t.TempDir()
	dir := androidupdate.Dir(dataDir)
	for _, sub := range []string{"patches", "full"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// The "APKs" here are arbitrary bytes: nothing in this test interprets the
	// APK format — the server only serves files and compares hashes. The proof
	// that the patch reconstructs a real APK lives in
	// scripts/test-android-patches.sh, with real hdiffz/hpatchz.
	shaAPKNovo := hashHex([]byte("apk-versao-nova"))
	shaAPKBase := hashHex([]byte("apk-versao-antiga"))

	conteudoFull := bytes.Repeat([]byte("F"), 1000)
	conteudoPatch := bytes.Repeat([]byte("P"), 300)

	arquivoFull := "full/" + shaAPKNovo + ".hdiff"
	arquivoPatch := "patches/" + shaAPKBase + "-" + shaAPKNovo + ".hdiff"
	escreve(t, filepath.Join(dir, arquivoFull), conteudoFull)
	escreve(t, filepath.Join(dir, arquivoPatch), conteudoPatch)

	m := androidupdate.Manifest{
		SchemaVersion: androidupdate.SchemaVersion,
		GeneratedAt:   "2026-09-06T00:00:00Z",
		PackageID:     "tech.northwind.vpsm.app",
		PatchTool:     "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m",
		Latest: androidupdate.Release{
			VersionName: "0.1.6",
			VersionCode: 6,
			SHA256:      shaAPKNovo,
			SizeBytes:   31135416,
		},
		Full: androidupdate.Artifact{
			Kind: "full", File: arquivoFull,
			SizeBytes: int64(len(conteudoFull)), SHA256: hashHex(conteudoFull),
		},
		Patches: []androidupdate.Artifact{{
			Kind: "patch", File: arquivoPatch,
			SizeBytes: int64(len(conteudoPatch)), SHA256: hashHex(conteudoPatch),
			FromSHA256: shaAPKBase, FromVersionName: "0.1.5", FromVersionCode: 5,
		}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	escreve(t, filepath.Join(dir, androidupdate.ManifestName), raw)

	return labUpdate{
		dataDir: dataDir, shaAPKNovo: shaAPKNovo, shaAPKBase: shaAPKBase,
		conteudoFull: conteudoFull, arquivoFull: arquivoFull, arquivoPatch: arquivoPatch,
	}
}

func escreve(t *testing.T, caminho string, dados []byte) {
	t.Helper()
	if err := os.WriteFile(caminho, dados, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hashHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func pedeUpdate(t *testing.T, dataDir, query string) *httptest.ResponseRecorder {
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

// TestAppUpdate_BaseConhecida_DevolvePatch — the happy path: the app sends the
// hash of the APK it has installed and receives the patch for that exact base.
func TestAppUpdate_BaseConhecida_DevolvePatch(t *testing.T) {
	lab := montaLabUpdate(t)
	rec := pedeUpdate(t, lab.dataDir, "?base_sha256="+lab.shaAPKBase)
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
	wantURL := Prefix + artifactPath + "?file=" + url.QueryEscape(lab.arquivoPatch)
	if got.Patch.URL != wantURL {
		t.Fatalf("patch.url = %q, want %q", got.Patch.URL, wantURL)
	}
	// The full path ALWAYS comes along: if applying the patch fails on the
	// device, the app does not need a second trip to the server.
	if got.Full.SizeBytes != 1000 {
		t.Fatalf("full.size_bytes = %d, want 1000", got.Full.SizeBytes)
	}
	if got.Latest.SHA256 != lab.shaAPKNovo || got.Latest.VersionCode != 6 {
		t.Fatalf("latest inesperado: %+v", got.Latest)
	}
	if got.PatchTool == "" {
		t.Fatal("patch_tool empty — the format-incompatibility diagnosis is lost")
	}
}

// TestAppUpdate_BaseDesconhecida_PatchNuloEFullPresente — the central guarantee
// of keying by hash: a base we do not have NEVER turns into an approximate
// patch. It is "no patch" + the full path.
func TestAppUpdate_BaseDesconhecida_PatchNuloEFullPresente(t *testing.T) {
	lab := montaLabUpdate(t)
	desconhecida := hashHex([]byte("apk-que-o-servidor-nunca-viu"))
	rec := pedeUpdate(t, lab.dataDir, "?base_sha256="+desconhecida)
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
		t.Fatalf("full incompleto: %+v", got.Full)
	}
}

// TestAppUpdate_SemBase_SoOCaminhoCompleto — fresh install/APK of unknown
// origin: with no base_sha256 there is nothing to match.
func TestAppUpdate_SemBase_SoOCaminhoCompleto(t *testing.T) {
	lab := montaLabUpdate(t)
	got := decodeUpdate(t, pedeUpdate(t, lab.dataDir, ""))
	if got.Patch != nil {
		t.Fatalf("patch = %+v without base_sha256 — should be null", got.Patch)
	}
	if got.UpToDate {
		t.Fatal("up_to_date = true without base_sha256")
	}
}

// TestAppUpdate_JaNaUltimaVersao — an app that is already up to date needs to
// learn that without downloading anything.
func TestAppUpdate_JaNaUltimaVersao(t *testing.T) {
	lab := montaLabUpdate(t)
	got := decodeUpdate(t, pedeUpdate(t, lab.dataDir, "?base_sha256="+lab.shaAPKNovo))
	if !got.UpToDate {
		t.Fatal("up_to_date = false for someone who already has the newest APK")
	}
	if got.Patch != nil {
		t.Fatalf("patch = %+v for someone who is already on the latest version", got.Patch)
	}
}

// TestAppUpdate_HashEmMaiuscula — the app may compute the hash in any case; the
// match must not depend on that.
func TestAppUpdate_HashEmMaiuscula(t *testing.T) {
	lab := montaLabUpdate(t)
	maiuscula := ""
	for _, r := range lab.shaAPKBase {
		if r >= 'a' && r <= 'f' {
			r = r - 'a' + 'A'
		}
		maiuscula += string(r)
	}
	got := decodeUpdate(t, pedeUpdate(t, lab.dataDir, "?base_sha256="+maiuscula))
	if got.Patch == nil {
		t.Fatal("patch = null with the same hash in uppercase")
	}
}

// TestAppUpdate_SemManifesto_503 — before the first publication. It is a normal
// state of the server, not "not found": the app needs to tell "channel not
// published yet" apart from "wrong resource".
func TestAppUpdate_SemManifesto_503(t *testing.T) {
	rec := pedeUpdate(t, t.TempDir(), "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAppUpdate_SemAutenticacao_401 — the patch reveals which parts of the code
// changed; the repository is private. It may never leave without a session.
func TestAppUpdate_SemAutenticacao_401(t *testing.T) {
	lab := montaLabUpdate(t)
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: lab.dataDir}})

	for _, alvo := range []string{
		"/api/mobile/v1/app/update?base_sha256=" + lab.shaAPKBase,
		"/api/mobile/v1/app/update/artifact?file=" + url.QueryEscape(lab.arquivoFull),
	} {
		req := httptest.NewRequest(http.MethodGet, alvo, nil) // no auth.WithUser
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", alvo, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("hdiff")) {
			t.Fatalf("%s: unauthenticated response leaks artifact name: %s", alvo, rec.Body.String())
		}
	}
}

func pedeArtefato(t *testing.T, dataDir, file string, header map[string]string) *httptest.ResponseRecorder {
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

// TestAppUpdateArtifact_Range_206 is the test that justifies the route existing
// with ServeContent: without Range, a 10 MB download on a bad connection
// restarts from zero forever.
func TestAppUpdateArtifact_Range_206(t *testing.T) {
	lab := montaLabUpdate(t)
	rec := pedeArtefato(t, lab.dataDir, lab.arquivoFull, map[string]string{"Range": "bytes=100-199"})

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body=%s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("bytes 100-199/%d", len(lab.conteudoFull))
	if got := rec.Header().Get("Content-Range"); got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), lab.conteudoFull[100:200]) {
		t.Fatalf("body of %d bytes is not the requested chunk", rec.Body.Len())
	}
}

// TestAppUpdateArtifact_RetomadaDoMeio — the shape the app actually uses when
// resuming: "I already have N bytes, send from N onwards".
func TestAppUpdateArtifact_RetomadaDoMeio(t *testing.T) {
	lab := montaLabUpdate(t)
	rec := pedeArtefato(t, lab.dataDir, lab.arquivoFull, map[string]string{"Range": "bytes=600-"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if rec.Body.Len() != 400 {
		t.Fatalf("body = %d bytes, want 400", rec.Body.Len())
	}
	if !bytes.Equal(rec.Body.Bytes(), lab.conteudoFull[600:]) {
		t.Fatal("resumed bytes do not match the tail of the file")
	}
}

// TestAppUpdateArtifact_SemRange_200Completo — the common case is still a whole
// download, with the correct Content-Length.
func TestAppUpdateArtifact_SemRange_200Completo(t *testing.T) {
	lab := montaLabUpdate(t)
	rec := pedeArtefato(t, lab.dataDir, lab.arquivoFull, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != len(lab.conteudoFull) {
		t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(lab.conteudoFull))
	}
	if got := rec.Header().Get("ETag"); got != `"`+hashHex(lab.conteudoFull)+`"` {
		t.Fatalf("ETag = %q — needs to be the artifact's sha256 for the resume's If-Range to work", got)
	}
}

// TestAppUpdateArtifact_RangeImpossivel_416 — a range past the end must not turn
// into a silent 200 with the entire file.
func TestAppUpdateArtifact_RangeImpossivel_416(t *testing.T) {
	lab := montaLabUpdate(t)
	rec := pedeArtefato(t, lab.dataDir, lab.arquivoFull, map[string]string{"Range": "bytes=99999-199999"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", rec.Code)
	}
}

// TestAppUpdateArtifact_ForaDoManifesto_404 — the route opens a file by a name
// that came off the network. The manifest is the allowlist: what is not in it
// does not exist, even if it exists on disk. Includes the classic traversal
// attempt.
func TestAppUpdateArtifact_ForaDoManifesto_404(t *testing.T) {
	lab := montaLabUpdate(t)

	// A real file, in the updates directory, but absent from the manifest.
	escreve(t, filepath.Join(androidupdate.Dir(lab.dataDir), "full", "intruso.hdiff"), []byte("x"))
	// And a plausible secret outside the directory, the target of a traversal.
	escreve(t, filepath.Join(lab.dataDir, "secrets.vault"), []byte("SEGREDO"))

	for _, nome := range []string{
		"full/intruso.hdiff",
		"../secrets.vault",
		"../../etc/passwd",
		"/etc/passwd",
		"full/../../secrets.vault",
		"",
	} {
		rec := pedeArtefato(t, lab.dataDir, nome, nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("file=%q returned 200 — the manifest allowlist did not hold", nome)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("SEGREDO")) {
			t.Fatalf("file=%q leaked content from outside the updates directory", nome)
		}
	}
}
