package webassets

import "net/http"

// HandleManifest serves the desktop PWA manifest. It allows installing as a
// native app on phone/desktop (its own icon on the home screen/dock, no URL bar).
// Chrome requires 192x192 + 512x512 PNG icons for the install prompt to appear.
func HandleManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(`{
  "id": "/",
  "name": "VPS Manager",
  "short_name": "vpsm",
  "description": "Painel de controle da VPS",
  "start_url": "/",
  "scope": "/",
  "lang": "pt-BR",
  "dir": "ltr",
  "display": "standalone",
  "display_override": ["standalone", "minimal-ui"],
  "orientation": "any",
  "background_color": "#020617",
  "theme_color": "#020617",
  "categories": ["productivity", "utilities"],
  "icons": [
    { "src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any" },
    { "src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any" },
    { "src": "/icon-512-maskable.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}`))
}
