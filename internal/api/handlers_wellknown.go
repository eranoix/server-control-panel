package api

import (
	"net/http"
)

type assetLinkTarget struct {
	Namespace              string   `json:"namespace"`
	PackageName            string   `json:"package_name"`
	SHA256CertFingerprints []string `json:"sha256_cert_fingerprints"`
}

type assetLinkEntry struct {
	Relation []string        `json:"relation"`
	Target   assetLinkTarget `json:"target"`
}

func (r *Router) handleAssetLinks(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.cfgMu.Lock()
	pkg := r.cfg.AndroidPackageName
	fps := append([]string(nil), r.cfg.AndroidSigningFingerprints...)
	r.cfgMu.Unlock()

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
