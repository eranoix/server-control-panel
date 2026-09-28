package api

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

const fdroidRepoFingerprintKey = "fdroid_repo_fingerprint"

const androidPackageID = "tech.northwind.servercontrolpanel"

type androidInstallPageData struct {
	RepoPublished bool
	Fingerprint   string
	AddRepoURL    string
	QRDataURL     template.URL
	HasRelease    bool
	ApkURL        string
	VersionName   string
	VersionCode   int64
}

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
			log.Printf("android-install: error generating the QR code: %v", err)
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
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
		log.Printf("android-install: error parsing the template: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = tpl.Execute(w, data)
}

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
	rel := strings.TrimPrefix(bestFile, "/")
	return "/fdroid/repo/" + rel, bestName, bestCode, true
}
