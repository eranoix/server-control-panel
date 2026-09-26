package api

// handlers_code.go — native VSCode (code-server) served gated under
// /_code, embedded as the "VSCode" sub-tab of the Dev page.
//
// code-server runs as a persistent systemd service (vpsm-code-server.service),
// bound EXCLUSIVELY to 127.0.0.1:8770 — never a public port. The only access
// path is this route, behind auth.Middleware + the mustPrimary gate. Since the
// editor hands out a ROOT shell over the web, the mustPrimary gate is
// MANDATORY — the defence is socket-root-only + the admin gate. NOTE:
// mustPrimary = IsAdmin (the primary OR any admin), so today ANY admin gets in
// and admins share the same editor/sessions. Restricting it to cfg.Primary is
// an open decision.
//
// Sub-path (the critical detail): code-server 4.x emits RELATIVE asset URLs
// (./_static/...) and a relative redirect (./?folder=...), with no <base> tag. So
// we strip the /_code prefix the same way browserProxy does with /browser: the
// iframe loads /_code/, the browser resolves ./_static/x → /_code/_static/x, and we
// strip it back to /_static/x upstream. (Verified: raw /_code/ upstream = 404;
// stripped / = 302 and /_static = 200.) No base-path in the config and no
// <base href> rewriting needed.

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// codeServerSocket is the root-only UNIX SOCKET of the systemd service
// vpsm-code-server. It matches --socket in ExecStart. Only root reaches it —
// this closes non-root co-tenants' access to the editor's API (the mustPrimary
// gate only covers traffic that goes through here).
const codeServerSocket = "/run/vpsm-code-server/code.sock"

// codeServerProxy reverse-proxies /_code/* to the local code-server on
// 127.0.0.1:8770, stripping the /_code prefix, and gating on the primary user.
//
// FlushInterval=-1: keeps code-server's WebSockets (terminal + editor) unbuffered
// — httputil.ReverseProxy handles Connection: Upgrade natively (Go 1.12+), the
// same mechanism that serves the embedded browser's WS.
//
// Since all traffic is same-origin (the panel serves /_code on its own origin),
// code-server's origin check passes: Origin and Host match.
//
// IMPORTANT (comment armor against /heal and refactors): this handler is registered
// in registerRoutes ("/_code/") under auth.Middleware. Removing it — or dropping the
// mustPrimary gate below — breaks TestSmokeCodeGated and exposes a root shell. Do not
// delete it without updating the test and the route. Protected by an invariant.
func (r *Router) codeServerProxy() http.Handler {
	// The upstream is a root-only unix socket, not TCP. The URL's host is
	// ignored — DialContext forces the dial onto the socket. WS (terminal/editor) keeps
	// working: the ReverseProxy hijacks and copies bytes over the dialed conn.
	target, _ := url.Parse("http://unix")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", codeServerSocket)
		},
	}
	// -1 = flush immediately: needed for the terminal/editor WS and for streaming.
	proxy.FlushInterval = -1
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		// Strip /_code — code-server uses relative URLs, so the browser already
		// resolves the assets under /_code/ and we hand them upstream without the prefix.
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/_code")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		base(req)
	}
	// Isolation headers (defence in depth), as in browserPersistentProxy.
	// Only set when the upstream did not — avoids conflicting with code-server.
	proxy.ModifyResponse = func(resp *http.Response) error {
		h := resp.Header
		if h.Get("X-Frame-Options") == "" {
			// SAMEORIGIN: o iframe embarca no painel (same-origin), mas nenhum
			// site externo consegue embarcar o editor.
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
		if h.Get("Referrer-Policy") == "" {
			h.Set("Referrer-Policy", "no-referrer")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		// A friendly 502 when the systemd service is down (the editor is
		// independent of the vps-manager deploy, so it may be restarting).
		http.Error(w, "editor (code-server) unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Gate admin-only (mustPrimary = IsAdmin: primary ou admin). Escreve
		// 401/403 e audita a negada. Sem isto, qualquer autenticado teria shell root.
		if _, ok := r.mustPrimary(w, req); !ok {
			return
		}
		proxy.ServeHTTP(w, req)
	})
}
