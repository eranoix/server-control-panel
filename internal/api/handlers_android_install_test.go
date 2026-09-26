package api

// handlers_android_install_test.go — covers the auth gate, the rendered content
// (QR + fingerprint) and the assembly of the add-repo URL from the request's own
// Host (never a fixed hostname, since this project serves more than one public
// domain from the same binary).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const testFdroidFingerprint = "AABBCCDDEEFF00112233445566778899AABBCCDDEEFF00112233445566778899"

// TestHandleAndroidInstallPage — behaviours 1/2/3 of the install page.
func TestHandleAndroidInstallPage(t *testing.T) {
	t.Run("unauthenticated never sees fingerprint or QR", func(t *testing.T) {
		r := newSmokeRouter(t)
		if r.secrets == nil {
			t.Fatal("smoke router without secrets.Store — should not happen with JWTSecret configured")
		}
		if err := r.secrets.Set(fdroidRepoFingerprintKey, testFdroidFingerprint); err != nil {
			t.Fatalf("secrets.Set: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/android/install", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET /android/install without session: got %d, want 401", w.Code)
		}
		body := w.Body.String()
		if regexp.MustCompile(`(?i)data:image/png;base64,`).MatchString(body) {
			t.Fatal("unauthenticated response contains embedded QR — fingerprint leak")
		}
		if regexp.MustCompile(testFdroidFingerprint).MatchString(body) {
			t.Fatal("unauthenticated response contains the fingerprint in plain text")
		}
	})

	t.Run("authenticated sees 200 html with QR and fingerprint", func(t *testing.T) {
		r := newSmokeRouter(t)
		if err := r.secrets.Set(fdroidRepoFingerprintKey, testFdroidFingerprint); err != nil {
			t.Fatalf("secrets.Set: %v", err)
		}

		w := androidInstallReq(t, r, "vpsmanager.example.test")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /android/install autenticado: got %d, want 200; body=%s", w.Code, w.Body.String())
		}
		ct := w.Header().Get("Content-Type")
		if !regexp.MustCompile(`^text/html`).MatchString(ct) {
			t.Fatalf("Content-Type = %q, want text/html", ct)
		}
		body := w.Body.String()
		if !regexp.MustCompile(`data:image/png;base64,`).MatchString(body) {
			t.Fatalf("response does not contain embedded QR; body=%s", body)
		}
		m := regexp.MustCompile(`fingerprint=([0-9A-Fa-f]+)`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("response does not contain fingerprint= in the add-repo URL; body=%s", body)
		}
		fp := m[1]
		if len(fp) != 64 {
			t.Fatalf("fingerprint has %d characters, want 64", len(fp))
		}
		if fp != regexp.MustCompile(`[0-9A-F]+`).FindString(fp) {
			t.Fatalf("fingerprint %q is not uppercase hex", fp)
		}
	})

	t.Run("add-repo URL reflects the request Host, never hardcoded", func(t *testing.T) {
		r := newSmokeRouter(t)
		if err := r.secrets.Set(fdroidRepoFingerprintKey, testFdroidFingerprint); err != nil {
			t.Fatalf("secrets.Set: %v", err)
		}

		w1 := androidInstallReq(t, r, "panel.northwind.example")
		w2 := androidInstallReq(t, r, "panel.host01.example")
		if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
			t.Fatalf("expected 200/200, got %d/%d", w1.Code, w2.Code)
		}

		url1 := regexp.MustCompile(`https://[^"&<\s]+fingerprint=[0-9A-F]+`).FindString(w1.Body.String())
		url2 := regexp.MustCompile(`https://[^"&<\s]+fingerprint=[0-9A-F]+`).FindString(w2.Body.String())
		if url1 == "" || url2 == "" {
			t.Fatalf("could not find the add-repo URL in one of the responses: %q / %q", url1, url2)
		}
		if url1 == url2 {
			t.Fatalf("add-repo URL did not change between different hosts: %q", url1)
		}
		if !regexp.MustCompile(`^https://panel\.northwind\.example/fdroid/repo`).MatchString(url1) {
			t.Fatalf("URL 1 does not reflect the request's Host: %q", url1)
		}
		if !regexp.MustCompile(`^https://panel\.host01\.example/fdroid/repo`).MatchString(url2) {
			t.Fatalf("URL 2 does not reflect the request's Host: %q", url2)
		}
	})

	t.Run("empty repo renders gracefully with no APK link, never an error", func(t *testing.T) {
		r := newSmokeRouter(t)
		if err := r.secrets.Set(fdroidRepoFingerprintKey, testFdroidFingerprint); err != nil {
			t.Fatalf("secrets.Set: %v", err)
		}
		w := androidInstallReq(t, r, "vpsmanager.example.test")
		if w.Code != http.StatusOK {
			t.Fatalf("empty repo: got %d, want 200; body=%s", w.Code, w.Body.String())
		}
		if !regexp.MustCompile(`(?i)[Nn]o version published`).MatchString(w.Body.String()) {
			t.Fatalf("expected the no-version-published notice; body=%s", w.Body.String())
		}
	})

	t.Run("no fingerprint yet: page still 200, explains pending publication, no QR", func(t *testing.T) {
		r := newSmokeRouter(t)
		w := androidInstallReq(t, r, "vpsmanager.example.test")
		if w.Code != http.StatusOK {
			t.Fatalf("no fingerprint: got %d, want 200; body=%s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if regexp.MustCompile(`data:image/png;base64,`).MatchString(body) {
			t.Fatalf("page with no fingerprint in the vault should not render any QR; body=%s", body)
		}
	})
}

// androidInstallReq builds a valid session (vpsm_token cookie) and fires
// GET /android/install with the given Host.
func androidInstallReq(t *testing.T, r *Router, host string) *httptest.ResponseRecorder {
	t.Helper()
	tok, _, err := r.auth.Issue("sam", nil)
	if err != nil {
		t.Fatalf("auth.Issue: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/android/install", nil)
	req.Host = host
	req.AddCookie(&http.Cookie{Name: "vpsm_token", Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestAndroidPackageID ties the androidPackageID constant to the build's SINGLE
// source of truth (android/gradle.properties, vpsmanager.applicationId).
//
// Why a test and not just a comment: the wrong value produces no error symptom
// at all — latestAndroidRelease returns ok=false and the page says
// "nenhuma versão publicada ainda" even with a full repository. That was exactly
// the bug found (the constant had been born as
// "br.tech.vpsmanager.app"). Reading the build file instead of repeating the
// literal here is what makes the test fail if the applicationId changes on one
// side only — including for every new consumer of the constant (incremental
// patch generation would inherit the same silent bug).
func TestAndroidPackageID(t *testing.T) {
	const prop = "vpsmanager.applicationId"
	raw, err := os.ReadFile(filepath.Join("..", "..", "android", "gradle.properties"))
	if err != nil {
		t.Fatalf("could not read android/gradle.properties: %v", err)
	}
	var want string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prop+"=") {
			want = strings.TrimSpace(strings.TrimPrefix(line, prop+"="))
			break
		}
	}
	if want == "" {
		t.Fatalf("android/gradle.properties does not define %s — the source of truth disappeared", prop)
	}
	if androidPackageID != want {
		t.Fatalf("androidPackageID = %q, but android/gradle.properties says %q — index-v2.json is keyed by the real applicationId, so /android/install would never find the release", androidPackageID, want)
	}
}

// TestLatestAndroidReleaseAchaOPacoteReal is the behaviour test that the one
// above protects by construction: with an index-v2.json keyed by the production
// applicationId, latestAndroidRelease MUST find the highest versionCode. With
// the constant wrong, this test fails.
func TestLatestAndroidReleaseAchaOPacoteReal(t *testing.T) {
	dir := t.TempDir()
	idx := `{"packages":{"` + androidPackageID + `":{"versions":{` +
		`"aaa":{"manifest":{"versionName":"0.1.5","versionCode":105},"file":{"name":"/vpsmanager-0.1.5.apk"}},` +
		`"bbb":{"manifest":{"versionName":"0.1.6","versionCode":106},"file":{"name":"/vpsmanager-0.1.6.apk"}}` +
		`}}}}`
	if err := os.WriteFile(filepath.Join(dir, "index-v2.json"), []byte(idx), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	apkURL, name, code, ok := latestAndroidRelease(dir, androidPackageID)
	if !ok {
		t.Fatal("latestAndroidRelease returned ok=false for an index that contains the package — androidPackageID is wrong")
	}
	if code != 106 || name != "0.1.6" {
		t.Fatalf("wrong version: %s/%d, want 0.1.6/106", name, code)
	}
	if apkURL != "/fdroid/repo/vpsmanager-0.1.6.apk" {
		t.Fatalf("apkURL = %q", apkURL)
	}
}
