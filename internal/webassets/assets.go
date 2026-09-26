// Package webassets — packages the embedded front-end (//go:embed) and the
// HTTP handlers that serve the PWA assets (manifests, icons, service worker,
// public splash pages such as /join).
//
// Owns:
//   - web/* (HTML, vendor JS, fonts, icons, tailwind.css)
//   - buildStamp (replaces __VPSM_BUILD__ in index.html to force-purge a
//     localStorage left incompatible by a deploy)
//   - PWA handlers (desktop/mobile manifest, /sw.js, /icon-*.png)
//   - index injector + in-memory cache
package webassets

import (
	"embed"
	"io/fs"
	"strconv"
	"time"
)

//go:embed web/*
var FS embed.FS

// docsFS embeds the technical report HTML (.docs/) served by the gated /_docs
// route. It lives in a SEPARATE embed from web/* on purpose: the public
// FileServer mounts only web/*, so this file is NEVER servable without going
// through the mustPrimary gate. report.html is a GENERATED artifact — copied
// from ".docs/Documentacao Tecnica - VPS Manager.html" by `make build` (the
// docs-embed target). Do not edit it by hand.
//
//go:embed docs/report.html
var docsFS embed.FS

// DocsReport returns the HTML of the technical report (served gated at /_docs).
// It only errors when the artifact was not copied before the build.
func DocsReport() ([]byte, error) { return docsFS.ReadFile("docs/report.html") }

// BuildStamp identifies this build of the binary. Injected into the <meta
// name="vpsm-build"> of index.html so the front-end purges a localStorage left
// incompatible after each deploy. It is the boot time of the process — every
// systemctl restart becomes a new stamp.
var BuildStamp = strconv.FormatInt(time.Now().Unix(), 10)

// ProcessStartTime: used to version the SW cache (each build = a new
// processStartTime → a new cache version → an SW update).
var ProcessStartTime = time.Now()

// SubFS returns the file system with the "web/" prefix stripped, ready to use
// as the base of an http.FileServer.
func SubFS() fs.FS {
	sub, _ := fs.Sub(FS, "web")
	return sub
}

// ReadFile reads a file out of the embed (path like "web/index.html"). Used by
// handlers that need to post-process it (indexInjector).
func ReadFile(path string) ([]byte, error) {
	return FS.ReadFile(path)
}
