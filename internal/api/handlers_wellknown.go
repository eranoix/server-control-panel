package api

// handlers_wellknown.go — Digital Asset Links manifest for the native Android
// app.
//
// /.well-known/assetlinks.json is fetched by the Android OS itself (and by
// any client) over HTTPS WITHOUT credentials, to validate that this domain
// authorizes the app br.tech.vpsmanager.app to use App Links and the Credential
// Manager (native passkeys). A known pitfall of this project: a mistake here — 404,
// a redirect, or an auth wall — breaks the native passkey ceremony
// silently (no clear log points back here).
//
// Source of package_name/fingerprints: config.Config (see internal/config),
// populated from the release keystore — see
// docs/android-signing-keystore.md Section 7. While those fields are
// empty, this handler answers 200 with an empty array (a structurally
// valid manifest, "no app verified yet"), never 404/500 and
// never a placeholder fingerprint.

import (
	"net/http"
)

// assetLinkTarget is the "target" field of a Digital Asset Links entry.
type assetLinkTarget struct {
	Namespace              string   `json:"namespace"`
	PackageName            string   `json:"package_name"`
	SHA256CertFingerprints []string `json:"sha256_cert_fingerprints"`
}

// assetLinkEntry is one entry of assetlinks.json's root array.
type assetLinkEntry struct {
	Relation []string        `json:"relation"`
	Target   assetLinkTarget `json:"target"`
}

// handleAssetLinks serves /.well-known/assetlinks.json. A public route (outside
// auth.Middleware) by design — the verification is done by Android itself,
// with no user session. Read from the config at runtime (under cfgMu), never
// compiled into the binary: editing data/config.json + restarting already changes the
// answer.
func (r *Router) handleAssetLinks(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.cfgMu.Lock()
	pkg := r.cfg.AndroidPackageName
	fps := append([]string(nil), r.cfg.AndroidSigningFingerprints...)
	r.cfgMu.Unlock()

	// No package_name, or no fingerprint configured yet (the expected state
	// until the real keystore is populated): an empty
	// manifest, not an error. An empty array is a valid Digital
	// Asset Links answer ("no app verified").
	if pkg == "" || len(fps) == 0 {
		writeJSON(w, []assetLinkEntry{})
		return
	}

	writeJSON(w, []assetLinkEntry{
		{
			Relation: []string{
				"delegate_permission/common.handle_all_urls",
				"delegate_permission/common.get_login_creds",
			},
			Target: assetLinkTarget{
				Namespace:              "android_app",
				PackageName:            pkg,
				SHA256CertFingerprints: fps,
			},
		},
	})
}
