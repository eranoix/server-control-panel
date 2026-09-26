package api

// handlers_wellknown_test.go — /.well-known/assetlinks.json has to be public,
// with no redirect, Content-Type application/json, and has to serve an empty
// manifest (not 404/500) for as long as the real fingerprint has not been
// populated.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestAssetLinksPublicEmptyConfig — with no AndroidPackageName/Fingerprints
// configured (the current state), the route must answer 200 with an empty JSON
// array, demanding no Authorization and issuing no redirect.
func TestAssetLinksPublicEmptyConfig(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/.well-known/assetlinks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("response has Location=%q — assetlinks.json must NEVER redirect", loc)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body []assetLinkEntry
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty array (no real fingerprint configured), got %+v", body)
	}
}

// TestAssetLinksUnauthenticated — the route must not sit under auth.Middleware:
// a request with no cookie/Authorization at all has to be 200, never 401/403.
func TestAssetLinksUnauthenticated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/.well-known/assetlinks.json", nil)
	// Explicitamente NENHUM header de auth.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == 401 || w.Code == 403 {
		t.Fatalf("assetlinks.json required authentication (status %d) — breaks Android verification, which fetches without credentials", w.Code)
	}
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestAssetLinksMethodNotAllowed — only GET is accepted.
func TestAssetLinksMethodNotAllowed(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("POST", "/.well-known/assetlinks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// TestAssetLinksPopulatedConfig — when AndroidPackageName and
// AndroidSigningFingerprints are filled in, the route must assemble the
// spec-correct Digital Asset Links entry.
func TestAssetLinksPopulatedConfig(t *testing.T) {
	r := newSmokeRouter(t)

	r.cfgMu.Lock()
	r.cfg.AndroidPackageName = "br.tech.vpsmanager.app"
	r.cfg.AndroidSigningFingerprints = []string{
		"AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99",
	}
	r.cfgMu.Unlock()

	req := httptest.NewRequest("GET", "/.well-known/assetlinks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var body []assetLinkEntry
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(body), body)
	}
	entry := body[0]
	if entry.Target.Namespace != "android_app" {
		t.Fatalf("namespace = %q, want android_app", entry.Target.Namespace)
	}
	if entry.Target.PackageName != "br.tech.vpsmanager.app" {
		t.Fatalf("package_name = %q, want br.tech.vpsmanager.app", entry.Target.PackageName)
	}
	if len(entry.Target.SHA256CertFingerprints) != 1 {
		t.Fatalf("expected 1 fingerprint, got %+v", entry.Target.SHA256CertFingerprints)
	}
	foundHandleURLs, foundLoginCreds := false, false
	for _, rel := range entry.Relation {
		if rel == "delegate_permission/common.handle_all_urls" {
			foundHandleURLs = true
		}
		if rel == "delegate_permission/common.get_login_creds" {
			foundLoginCreds = true
		}
	}
	if !foundHandleURLs || !foundLoginCreds {
		t.Fatalf("relation incompleta: %+v", entry.Relation)
	}
}
