package api

// handlers_android_install.go — /android/install: an authenticated page inside
// the panel itself that gives the admin a complete path to install the Android
// app without leaving vps-manager. It shows a QR code (same mechanism as
// internal/api/handlers_auth.go's TOTP: qrcode.Encode + data URL) encoding
// the F-Droid add-repo URL pointing at the self-hosted repository
// (internal/api/handlers_fdroid.go), with the SHA-256 fingerprint of the
// repository's signing key embedded — exactly what the F-Droid client
// expects under "Repositórios → escanear QR".
//
// The fingerprint is read LIVE from data/secrets.vault (key
// fdroidRepoFingerprintKey), never copied into the code: if the repokey is
// rotated, this page reflects the new value on the next render, with no
// deploy needed. See docs/android-fdroid-repo.md §3/§4.
//
// Until the operator has generated the repokey offline (a manual step, see
// docs/android-fdroid-repo.md §6), the vault entry is absent — the page
// treats that as "repository not published yet", never as a 500 error nor
// as a broken/fake QR.

import (
	"encoding/base64"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"server-control-panel/internal/webassets"
)

// fdroidRepoFingerprintKey is the same vault key that
// docs/android-fdroid-repo.md §4 tells the operator to fill in
// (`vpsmctl secrets set --user sam fdroid_repo_fingerprint`) after
// generating the repokey offline.
const fdroidRepoFingerprintKey = "fdroid_repo_fingerprint"

// androidPackageID is the native app's applicationId — the key under which the
// package appears in `packages` of F-Droid's index-v2.json.
//
// Source of truth: android/gradle.properties (vpsmanager.applicationId), the
// ONLY place in the build where the literal exists, locked by the operator's
// decision in docs/android-signing-keystore.md §2. A wrong value here breaks
// nothing visibly — latestAndroidRelease simply does not find the key in the
// index and returns ok=false, and the /android/install page says
// "no version published yet" forever, even with a full repository.
// That is why this value has its own test (TestAndroidPackageID) pinning it
// to the documented value: any new code reusing the constant (e.g. incremental
// patch generation) would inherit the same silent bug.
const androidPackageID = "tech.northwind.vpsm.app"

// androidInstallPageData alimenta o template android-install.html.
type androidInstallPageData struct {
	RepoPublished bool // the fingerprint already exists in the vault (repokey generated and published)
	Fingerprint   string
	AddRepoURL    string
	QRDataURL     template.URL
	HasRelease    bool // index-v2.json has at least one version of androidPackageID
	ApkURL        string
	VersionName   string
	VersionCode   int64
}

// handleAndroidInstallPage serves GET /android/install. Registered under
// r.auth.Middleware in NewRouter — it never renders the fingerprint/QR to
// anyone without a valid session.
func (r *Router) handleAndroidInstallPage(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	data := androidInstallPageData{}

	var fingerprint string
	if r.secrets != nil {
		if v, ok := r.secrets.Get(fdroidRepoFingerprintKey); ok {
			fingerprint = strings.TrimSpace(v)
		}
	}
	data.RepoPublished = fingerprint != ""
	data.Fingerprint = fingerprint

	if data.RepoPublished {
		addRepoURL := "https://" + req.Host + "/fdroid/repo?fingerprint=" + fingerprint
		data.AddRepoURL = addRepoURL

		png, err := qrcode.Encode(addRepoURL, qrcode.Medium, 256)
		if err != nil {
			// Never echo err.Error() to the client: it may carry internal detail
			// from the encoder. The full error goes only to the server log.
			log.Printf("android-install: error generating the QR code: %v", err)
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		// data.QRDataURL is template.URL (not string) on purpose: html/template
		// treats "src" as a URL context and would sanitize a plain string,
		// corrupting the data URL. Same care as the TOTP pattern.
		data.QRDataURL = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))

		if apkPath, versionName, versionCode, ok := latestAndroidRelease(filepath.Join(r.cfg.DataDir, "fdroid", "repo"), androidPackageID); ok {
			data.HasRelease = true
			data.ApkURL = apkPath
			data.VersionName = versionName
			data.VersionCode = versionCode
		}
	}

	tplBytes, err := webassets.FS.ReadFile("web/android-install.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "install page template missing")
		return
	}
	tpl, err := template.New("android-install").Parse(string(tplBytes))
	if err != nil {
		// Never echo err.Error() to the client — same posture as above.
		log.Printf("android-install: error parsing the template: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = tpl.Execute(w, data)
}

// fdroidIndexV2 is the subset of fdroidserver's index-v2.json schema that
// this page needs: per package, the list of published versions, each with
// its manifest (versionName/versionCode) and the APK's file name.
// See https://f-droid.org/docs/Index_Format/ — we do not model the whole
// schema because we only use these fields.
type fdroidIndexV2 struct {
	Packages map[string]struct {
		Versions map[string]struct {
			Manifest struct {
				VersionName string `json:"versionName"`
				VersionCode int64  `json:"versionCode"`
			} `json:"manifest"`
			File struct {
				Name string `json:"name"`
			} `json:"file"`
		} `json:"versions"`
	} `json:"packages"`
}

// latestAndroidRelease reads <repoDir>/index-v2.json and returns the public
// path (under /fdroid/repo/) of the APK with the highest versionCode for
// pkgID. Any absence — missing file, malformed JSON, package with no
// published version yet — returns ok=false without an error: that index is
// generated elsewhere, and this page must stay correct and diagnosable
// before it exists, not break.
func latestAndroidRelease(repoDir, pkgID string) (apkURL, versionName string, versionCode int64, ok bool) {
	raw, err := os.ReadFile(filepath.Join(repoDir, "index-v2.json"))
	if err != nil {
		return "", "", 0, false
	}
	var idx fdroidIndexV2
	if err := json.Unmarshal(raw, &idx); err != nil {
		return "", "", 0, false
	}
	pkg, found := idx.Packages[pkgID]
	if !found {
		return "", "", 0, false
	}
	var bestCode int64 = -1
	var bestName, bestFile string
	for _, v := range pkg.Versions {
		if v.Manifest.VersionCode > bestCode {
			bestCode = v.Manifest.VersionCode
			bestName = v.Manifest.VersionName
			bestFile = v.File.Name
		}
	}
	if bestFile == "" {
		return "", "", 0, false
	}
	// index-v2.json stores the file name with a leading "/" (e.g.
	// "/app-release.apk"); normalize it to build the public URL without a double slash.
	rel := strings.TrimPrefix(bestFile, "/")
	return "/fdroid/repo/" + rel, bestName, bestCode, true
}
