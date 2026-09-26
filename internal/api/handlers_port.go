package api

import (
	"net/http"
	"net/http/httputil"
	"os/exec"
	"strconv"
	"strings"
)

// handlers_port.go — editor port forwarding. A dev process running in the
// code-server terminal (e.g. `npm run dev` on 5173, `python -m http.server 8000`)
// becomes reachable through a gated URL /_port/<n>/ that proxies to
// 127.0.0.1:<n>. Primary-only (same gate as the editor — it is root access to the VPS).
//
// Known limitation (path-based, not subdomain): apps that emit ABSOLUTE URLs
// (/asset) break under the sub-path — the robust alternative (one subdomain per
// port) would require wildcard DNS+TLS. For a genuinely publishable preview,
// use the Deploy tab (nginx vhost with a domain). WebSocket works (FlushInterval
// -1 + the ReverseProxy's native upgrade).

const portForwardPrefix = "/_port/"

// selfPort is vps-manager's own port — never proxy to it (loop).
const selfPort = 8765

// parsePortPath extrai a porta e o resto do caminho de /_port/<n>[/...].
// ok=false se malformado. needSlash=true quando falta a barra final
// (/_port/8000) — o chamador redireciona pra resolver assets relativos.
func parsePortPath(p string) (port int, rest string, needSlash, ok bool) {
	s := strings.TrimPrefix(p, portForwardPrefix)
	if s == p || s == "" {
		return 0, "", false, false
	}
	i := strings.IndexByte(s, '/')
	var digits string
	if i < 0 {
		digits = s
		needSlash = true
	} else {
		digits = s[:i]
		rest = s[i:]
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || n > 65535 {
		return 0, "", false, false
	}
	if rest == "" {
		rest = "/"
	}
	return n, rest, needSlash, true
}

func forwardablePort(n int) bool {
	// User ports only; never the control plane's own, nor <1024 (infra/root).
	return n >= 1024 && n <= 65535 && n != selfPort
}

// portForwardProxy serve /_port/<n>/... → 127.0.0.1:<n>. Gate primary-only.
func (r *Router) portForwardProxy() http.Handler {
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Director: func(req *http.Request) {
			port, rest, _, ok := parsePortPath(req.URL.Path)
			if !ok {
				return
			}
			req.URL.Scheme = "http"
			req.URL.Host = "127.0.0.1:" + strconv.Itoa(port)
			req.URL.Path = rest
			// nginx termina o TLS; sinaliza pro app upstream.
			if req.Header.Get("X-Forwarded-Proto") == "" {
				req.Header.Set("X-Forwarded-Proto", "https")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "port does not answer: "+err.Error(), http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, ok := r.mustPrimary(w, req); !ok {
			return
		}
		port, _, needSlash, ok := parsePortPath(req.URL.Path)
		if !ok || !forwardablePort(port) {
			writeErr(w, 400, "invalid or non-forwardable port (use 1024-65535, except the panel's)")
			return
		}
		if needSlash {
			http.Redirect(w, req, req.URL.Path+"/", http.StatusFound)
			return
		}
		proxy.ServeHTTP(w, req)
	})
}

// devPort is a port listening on the host.
type devPort struct {
	Port int    `json:"port"`
	Addr string `json:"addr"`
	Proc string `json:"proc"`
}

// GET /api/dev/ports → portas TCP em escuta (para o painel de forwarding).
func (r *Router) handleDevPorts(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	ports := listListeningPorts()
	if ports == nil {
		ports = []devPort{}
	}
	writeJSON(w, map[string]any{"ports": ports, "prefix": portForwardPrefix})
}

// listListeningPorts runs `ss -ltnpH` and extracts the forwardable ports, deduped
// by port. Silent failure → empty list.
func listListeningPorts() []devPort {
	out, err := exec.Command("ss", "-ltnpH").Output()
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var res []devPort
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[3] // ex.: 127.0.0.1:8000  ou  *:8000  ou  [::]:8000
		idx := strings.LastIndexByte(local, ':')
		if idx < 0 {
			continue
		}
		host := local[:idx]
		// Loopback binds only: those are the dev servers started in the editor's
		// terminal. Public ports (0.0.0.0/[::]) belong to containers and already have
		// their own access — listing them would be pure noise. (Manual forwarding covers the rest.)
		if host != "127.0.0.1" && host != "[::1]" && host != "::1" {
			continue
		}
		n, err := strconv.Atoi(local[idx+1:])
		if err != nil || !forwardablePort(n) || seen[n] {
			continue
		}
		seen[n] = true
		proc := ""
		if i := strings.Index(line, `users:(("`); i >= 0 {
			rest := line[i+len(`users:(("`):]
			if j := strings.IndexByte(rest, '"'); j >= 0 {
				proc = rest[:j]
			}
		}
		res = append(res, devPort{Port: n, Addr: local[:idx], Proc: proc})
	}
	return res
}
