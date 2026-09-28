package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

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

func TestAssetLinksUnauthenticated(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("GET", "/.well-known/assetlinks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == 401 || w.Code == 403 {
		t.Fatalf("assetlinks.json required authentication (status %d) — breaks Android verification, which fetches without credentials", w.Code)
	}
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestAssetLinksMethodNotAllowed(t *testing.T) {
	r := newSmokeRouter(t)

	req := httptest.NewRequest("POST", "/.well-known/assetlinks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestAssetLinksPopulatedConfig(t *testing.T) {
	r := newSmokeRouter(t)

	r.cfgMu.Lock()
	r.cfg.AndroidPackageName = "tech.northwind.servercontrolpanel"
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
	if entry.Target.PackageName != "tech.northwind.servercontrolpanel" {
		t.Fatalf("package_name = %q, want tech.northwind.servercontrolpanel", entry.Target.PackageName)
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
		t.Fatalf("incomplete relation: %+v", entry.Relation)
	}
}
